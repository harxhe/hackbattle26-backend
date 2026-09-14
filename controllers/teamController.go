package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/v4/auth"
	"github.com/IEEECS-VIT/hackbattle25-backend/config"
	"github.com/IEEECS-VIT/hackbattle25-backend/middleware"
	"github.com/IEEECS-VIT/hackbattle25-backend/models"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type TeamPayload struct {
	Name string `json:"name"`
}

const maxTeamSize = 5
const minTeamSize = 2

type httpError struct {
	message string
	code    int
}

func (e *httpError) Error() string {
	return e.message
}

func handleFirestoreError(w http.ResponseWriter, err error) {
	if e, ok := err.(*httpError); ok {
		http.Error(w, e.message, e.code)
		return
	}
	st := status.Convert(err)
	switch st.Code() {
	case codes.NotFound:
		http.Error(w, st.Message(), http.StatusNotFound)
	case codes.AlreadyExists:
		http.Error(w, st.Message(), http.StatusConflict)
	case codes.FailedPrecondition:
		http.Error(w, st.Message(), http.StatusForbidden)
	default:
		http.Error(w, "An unexpected error occurred: "+st.Message(), http.StatusInternalServerError)
	}
}

func getUserEmailFromContext(r *http.Request) (string, bool) {
	token, ok := r.Context().Value(middleware.UserKey).(*auth.Token)
	if !ok {
		return "", false
	}
	email, ok := token.Claims["email"].(string)
	if !ok {
		return "", false
	}
	return strings.ToLower(email), true
}

func verifyTeamLeader(ctx context.Context, r *http.Request) (string, error) {
	userEmail, ok := getUserEmailFromContext(r)
	if !ok {
		return "", &httpError{"Invalid token: missing email", http.StatusUnauthorized}
	}

	userDoc, err := config.FirestoreClient.Collection("users").Doc(userEmail).Get(ctx)
	if err != nil {
		return "", &httpError{"User profile not found", http.StatusNotFound}
	}

	isLead, err := userDoc.DataAt("IsLead")
	if err != nil || !isLead.(bool) {
		return "", &httpError{"User is not a team leader", http.StatusForbidden}
	}

	teamIDRaw, err := userDoc.DataAt("TeamID")
	if err != nil {
		return "", &httpError{"Team ID not found for leader", http.StatusInternalServerError}
	}

	var teamID string
	switch v := teamIDRaw.(type) {
	case string:
		teamID = v
	case *firestore.DocumentRef:
		teamID = v.ID
	default:
		return "", &httpError{"TeamID field is invalid", http.StatusInternalServerError}
	}
	if teamID == "" {
		return "", &httpError{"Team ID not found for leader", http.StatusInternalServerError}
	}

	return teamID, nil
}

func generateTeamCode(ctx context.Context, teamsCollection *firestore.CollectionRef) (string, error) {
	for {
		code := randomTeamCode()
		doc, err := teamsCollection.Doc(code).Get(ctx)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return code, nil
			}
			return "", err
		}
		if !doc.Exists() {
			return code, nil
		}
	}
}

func randomTeamCode() string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rand.Seed(time.Now().UnixNano())
	b := make([]byte, 6)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	return string(b)
}

func CreateTeam(w http.ResponseWriter, r *http.Request) {
	userEmail, ok := getUserEmailFromContext(r)
	if !ok {
		http.Error(w, "Invalid token: missing email", http.StatusUnauthorized)
		return
	}

	var payload TeamPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Name == "" {
		http.Error(w, "Invalid team name", http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	userRef := config.FirestoreClient.Collection("users").Doc(userEmail)
	teamsCollection := config.FirestoreClient.Collection("teams")

	// Check if team name already exists
	q := teamsCollection.Where("Name", "==", payload.Name).Limit(1)
	if docs, _ := q.Documents(ctx).GetAll(); len(docs) > 0 {
		http.Error(w, "This team name is already taken", http.StatusConflict)
		return
	}

	teamCode, _ := generateTeamCode(ctx, teamsCollection)

	var alreadyInTeam bool

	err := config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		userDoc, err := tx.Get(userRef)
		if err != nil {
			return status.Errorf(codes.NotFound, "User profile not found")
		}

		if teamID, _ := userDoc.DataAt("TeamID"); teamID != nil {
			alreadyInTeam = true
			return nil // stop here, don’t create a team
		}

		userName, _ := userDoc.Data()["name"]
		name, ok := userName.(string)
		if !ok || name == "" {
			return status.Errorf(codes.Internal, "User name not found in user document")
		}

		newTeamRef := teamsCollection.Doc(teamCode)
		if err := tx.Set(newTeamRef, map[string]interface{}{
			"Name":      payload.Name,
			"Code":      teamCode,
			"leaderId":  userEmail,
			"members":   []map[string]interface{}{{"email": userEmail, "name": name}},
			"CreatedAt": time.Now(),
		}); err != nil {
			return err
		}

		return tx.Update(userRef, []firestore.Update{
			{Path: "TeamID", Value: newTeamRef.ID},
			{Path: "IsLead", Value: true},
		})
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	if alreadyInTeam {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"message": "You are already in a team"})
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Team created successfully",
		"code":    teamCode,
	})
}

// JoinTeam adds email+name as a member
func JoinTeam(w http.ResponseWriter, r *http.Request) {
	userEmail, ok := getUserEmailFromContext(r)
	if !ok {
		http.Error(w, "Invalid token: missing email", http.StatusUnauthorized)
		return
	}

	var req struct {
		TeamCode string `json:"team_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TeamCode == "" {
		w.WriteHeader(http.StatusNoContent)
		json.NewEncoder(w).Encode(map[string]string{"message": "Invalid or missing team code"})
		return
	}

	req.TeamCode = strings.ToUpper(req.TeamCode)
	ctx := context.Background()
	userRef := config.FirestoreClient.Collection("users").Doc(userEmail)
	teamRef := config.FirestoreClient.Collection("teams").Doc(req.TeamCode)

	var statusCode int
	var message string

	err := config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		userDoc, err := tx.Get(userRef)
		if err != nil {
			return status.Errorf(codes.NotFound, "User profile not found")
		}
		if teamID, _ := userDoc.DataAt("TeamID"); teamID != nil {
			statusCode = http.StatusCreated
			message = "You are already in a team"
			return nil
		}

		teamSnap, err := tx.Get(teamRef)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				statusCode = http.StatusNoContent
				message = "Team with that code not found"
				return nil
			}
			return err
		}

		if _, err := teamSnap.DataAt("SubmittedAt"); err == nil {
			statusCode = http.StatusForbidden
			message = "Team has already submitted. No new members can join."
			return nil
		}

		members, _ := teamSnap.DataAt("members")
		if len(members.([]interface{})) >= maxTeamSize {
			statusCode = http.StatusAlreadyReported
			message = "Team at max size"
			return nil
		}

		userName, _ := userDoc.Data()["name"]
		name, ok := userName.(string)
		if !ok || name == "" {
			return status.Errorf(codes.Internal, "User name not found in user document")
		}
		updates := []firestore.Update{
			{Path: "members", Value: firestore.ArrayUnion(map[string]interface{}{"email": userEmail, "name": name})},
		}

		if err := tx.Update(teamRef, updates); err != nil {
			return err
		}
		return tx.Update(userRef, []firestore.Update{{Path: "TeamID", Value: teamRef.ID}})
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	if statusCode != 0 {
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(map[string]string{"message": message})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "User joined team successfully"})
}

func LeaveOrDeleteTeam(w http.ResponseWriter, r *http.Request) {
	userEmail, ok := getUserEmailFromContext(r)
	if !ok {
		http.Error(w, "Invalid token: missing email", http.StatusUnauthorized)
		return
	}

	ctx := context.Background()
	userRef := config.FirestoreClient.Collection("users").Doc(userEmail)
	var teamID string

	
	err := config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		userDoc, err := tx.Get(userRef)
		if err != nil {
			return status.Errorf(codes.NotFound, "User profile not found")
		}


		teamIDData, _ := userDoc.DataAt("TeamID")
		if teamIDData == nil {
			return status.Errorf(codes.FailedPrecondition, "User is not in a team")
		}

		// safely extract teamID
		switch v := teamIDData.(type) {
		case string:
			teamID = v
		case *firestore.DocumentRef:
			teamID = v.ID
		default:
			return status.Errorf(codes.Internal, "TeamID field is invalid")
		}

		// Get user's IsLead status
		IsLeadData, _ := userDoc.DataAt("IsLead")
		IsLead := false
		if lead, ok := IsLeadData.(bool); ok {
			IsLead = lead
		}

		teamRef := config.FirestoreClient.Collection("teams").Doc(teamID)
		teamDoc, err := tx.Get(teamRef)
		if err != nil {
			return status.Errorf(codes.NotFound, "Team not found")
		}
		if _, err := teamDoc.DataAt("SubmittedAt"); err == nil {
			return status.Errorf(codes.FailedPrecondition, "Team has already submitted. No member can leave the team.")
		}
		membersData, _ := teamDoc.DataAt("members")
		members := membersData.([]interface{})

		// Get user's name from their user document for the ArrayRemove operation
		userName := userDoc.Data()["name"]
		if userName == nil {
			return status.Errorf(codes.Internal, "User name not found in user document")
		}
		log.Printf("IsLead: %v\n", IsLead)
		// Logic for a Team Leader
		if IsLead {
			if len(members) > 1 {
				// Transfer leadership to the next member
				var newLeadEmail string
				for _, member := range members {
					memberMap := member.(map[string]interface{})
					if memberMap["email"].(string) != userEmail {
						newLeadEmail = memberMap["email"].(string)
						break
					}
				}

				if newLeadEmail == "" {
					return status.Errorf(codes.Internal, "Could not find a new leader.")
				}

				newLeaderRef := config.FirestoreClient.Collection("users").Doc(newLeadEmail)
				if err := tx.Update(newLeaderRef, []firestore.Update{{Path: "IsLead", Value: true}}); err != nil {
					return err
				}

				// Remove leaving leader manually
				newMembers := []interface{}{}
				for _, member := range members {
					memberMap := member.(map[string]interface{})
					if memberMap["email"].(string) != userEmail {
						newMembers = append(newMembers, memberMap)
					}
				}

				// Update leaderId and members in a single write (a transaction cannot write the same document twice)
				if err := tx.Update(teamRef, []firestore.Update{
					{Path: "leaderId", Value: newLeadEmail},
					{Path: "members", Value: newMembers},
				}); err != nil {
					return err
				}

				// Update the original leader's user document
				return tx.Update(userRef, []firestore.Update{
					{Path: "TeamID", Value: firestore.Delete},
					{Path: "IsLead", Value: firestore.Delete},
				})
			} else {
				// The leader is the only one left, so delete the team
				if err := tx.Delete(teamRef); err != nil {
					return err
				}

				// Update the leader's user document
				return tx.Update(userRef, []firestore.Update{
					{Path: "TeamID", Value: firestore.Delete},
					{Path: "IsLead", Value: firestore.Delete},
				})
			}
		}

	
		newMembers := []interface{}{}
		for _, member := range members {
			memberMap := member.(map[string]interface{})
			if memberMap["email"].(string) != userEmail {
				newMembers = append(newMembers, memberMap)
			}
		}
		if err := tx.Update(teamRef, []firestore.Update{
			{Path: "members", Value: newMembers},
		}); err != nil {
			return err
		}

		return tx.Update(userRef, []firestore.Update{
			{Path: "TeamID", Value: nil},
			{Path: "IsLead", Value: firestore.Delete},
		})
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Action completed successfully"})
}

// LeaveTeam removes email+name
func LeaveTeam(w http.ResponseWriter, r *http.Request) {
	userEmail, ok := getUserEmailFromContext(r)
	if !ok {
		http.Error(w, "Invalid token: missing email", http.StatusUnauthorized)
		return
	}

	ctx := context.Background()
	userRef := config.FirestoreClient.Collection("users").Doc(userEmail)

	err := config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		userDoc, err := tx.Get(userRef)
		if err != nil {
			return status.Errorf(codes.NotFound, "User profile not found")
		}

		teamID, _ := userDoc.DataAt("TeamID")
		if teamID == nil {
			return status.Errorf(codes.FailedPrecondition, "User is not in a team")
		}

		isLeadData, _ := userDoc.DataAt("IsLead")
		if isLead, ok := isLeadData.(bool); ok && isLead {
			return status.Errorf(codes.FailedPrecondition, "Leaders cannot leave a team. Delete team or transfer leadership.")
		}

		var teamIDStr string
		switch v := teamID.(type) {
		case string:
			teamIDStr = v
		case *firestore.DocumentRef:
			teamIDStr = v.ID
		default:
			return status.Errorf(codes.Internal, "TeamID field is invalid")
		}
		teamRef := config.FirestoreClient.Collection("teams").Doc(teamIDStr)
		teamUpdates := []firestore.Update{
			{Path: "members", Value: firestore.ArrayRemove(map[string]interface{}{"email": userEmail, "name": userDoc.Data()["name"]})},
		}
		if err := tx.Update(teamRef, teamUpdates); err != nil {
			return err
		}

		return tx.Update(userRef, []firestore.Update{
			{Path: "TeamID", Value: firestore.Delete},
			{Path: "IsLead", Value: firestore.Delete},
		})
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Successfully left team"})
}

// RemoveMember removes by email+name
func RemoveMember(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	var payload struct {
		MemberEmail string `json:"memberEmail"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.MemberEmail == "" {
		http.Error(w, "Invalid member email provided", http.StatusBadRequest)
		return
	}

	teamID, err := verifyTeamLeader(ctx, r)
	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	if payload.MemberEmail == "" {
		http.Error(w, "Member email required", http.StatusBadRequest)
		return
	}

	teamRef := config.FirestoreClient.Collection("teams").Doc(teamID)
	memberRef := config.FirestoreClient.Collection("users").Doc(payload.MemberEmail)

	err = config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		memberDoc, err := tx.Get(memberRef)
		if err != nil {
			return &httpError{"Member user profile not found", http.StatusNotFound}
		}

		teamDoc, err := tx.Get(teamRef)
		if err != nil {
			return &httpError{"Team not found", http.StatusNotFound}
		}
		if _, err := teamDoc.DataAt("SubmittedAt"); err == nil {
			return &httpError{"Team has already submitted. No members can be removed.", http.StatusForbidden}
		}

		memberName, _ := memberDoc.DataAt("name")

		teamUpdates := []firestore.Update{
			{Path: "members", Value: firestore.ArrayRemove(map[string]interface{}{"email": payload.MemberEmail, "name": memberName})},
		}
		if err := tx.Update(teamRef, teamUpdates); err != nil {
			return err
		}

		return tx.Update(memberRef, []firestore.Update{
			{Path: "TeamID", Value: nil},
			{Path: "IsLead", Value: false},
		})
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Member removed successfully"})
}

func DeleteTeam(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	teamID, err := verifyTeamLeader(ctx, r)
	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	userEmail, _ := getUserEmailFromContext(r)
	teamRef := config.FirestoreClient.Collection("teams").Doc(teamID)

	err = config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		teamDoc, err := tx.Get(teamRef)
		if err != nil {
			return &httpError{"Team not found", http.StatusNotFound}
		}

		if _, err := teamDoc.DataAt("SubmittedAt"); err == nil {
			return &httpError{"Team has already submitted and cannot be deleted.", http.StatusForbidden}
		}

		membersData, _ := teamDoc.DataAt("members")
		members, _ := membersData.([]interface{})

		// Clear TeamID and IsLead for all members
		for _, member := range members {
			memberMap, ok := member.(map[string]interface{})
			if !ok {
				continue
			}
			email, _ := memberMap["email"].(string)
			if email == "" {
				continue
			}
			memberRef := config.FirestoreClient.Collection("users").Doc(email)
			updates := []firestore.Update{
				{Path: "TeamID", Value: firestore.Delete},
				{Path: "IsLead", Value: firestore.Delete},
			}
			if err := tx.Update(memberRef, updates); err != nil {
				return err
			}
		}

		_ = userEmail
		return tx.Delete(teamRef)
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Team deleted successfully"})
}

// GetTeam returns full team details including Track & Subtrack
func GetTeam(w http.ResponseWriter, r *http.Request) {
	userEmail, ok := getUserEmailFromContext(r)
	if !ok {
		http.Error(w, "Invalid token: missing email", http.StatusUnauthorized)
		return
	}

	ctx := context.Background()

	userDoc, err := config.FirestoreClient.Collection("users").Doc(userEmail).Get(ctx)
	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	teamIDValue, _ := userDoc.DataAt("TeamID")
	if teamIDValue == nil {
		w.WriteHeader(http.StatusNoContent)
		json.NewEncoder(w).Encode(map[string]string{"message": "User is not part of any team"})
		return
	}
	var teamID string
	switch v := teamIDValue.(type) {
	case string:
		teamID = v
	case *firestore.DocumentRef:
		teamID = v.ID
	default:
		http.Error(w, "Invalid TeamID field", http.StatusInternalServerError)
		return
	}

	teamDoc, err := config.FirestoreClient.Collection("teams").Doc(teamID).Get(ctx)
	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	var teamData models.Team
	if err := teamDoc.DataTo(&teamData); err != nil {
		http.Error(w, "Failed to parse team data", http.StatusInternalServerError)
		return
	}

	membersList, _ := teamDoc.DataAt("members")

	response := map[string]interface{}{
		"id":           teamDoc.Ref.ID,
		"name":         teamData.Name,
		"code":         teamData.Code,
		"leaderId":     teamData.LeaderID,
		"members":      membersList,
		"track":        teamData.Track,
		"subtrack":     teamData.Subtrack,
		"project_desc": teamData.ProjectDesc,
		"github_link":  teamData.GithubLink,
		"figma_link":   teamData.FigmaLink,
		"other_files":  teamData.OtherFiles,
		"submitted_at": teamData.SubmittedAt,
		"updated_at":   teamData.UpdatedAt,
		"isLeader":     teamData.LeaderID == userEmail,
	}

	// Priority: if isQualifiedForFinalRound exists in Firestore, send only that.
	// Else if isQualifiedForR3 exists, send only that.
	// Otherwise omit both fields entirely.
	rawData := teamDoc.Data()
	if finalVal, hasFinal := rawData["isQualifiedForFinalRound"]; hasFinal {
		if v, ok := finalVal.(bool); ok {
			response["isQualifiedForFinalRound"] = v
		}
	} else if r3Val, hasR3 := rawData["isQualifiedForR3"]; hasR3 {
		if v, ok := r3Val.(bool); ok {
			response["isQualifiedForR3"] = v
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// SubmissionPayload defines the payload for project submissions and updates
type SubmissionPayload struct {
	ProjectDesc *string `json:"project_desc"`
	GithubLink  *string `json:"github_link"`
	FigmaLink   *string `json:"figma_link"`
	OtherFiles  *string `json:"other_files"`
	Track       *string `json:"track,omitempty"`
	Subtrack    *string `json:"subtrack,omitempty"`
}

// TrackPayload for saving/updating Track and optional Subtrack independently
type TrackPayload struct {
	Track    string  `json:"track"`
	Subtrack *string `json:"subtrack"` // Pointer allows nil when subtrack isn't required
}

func UpdateTrack(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	var payload TrackPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Track == "" {
		http.Error(w, "Track is required", http.StatusBadRequest)
		return
	}

	teamID, err := verifyTeamLeader(ctx, r)
	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	teamRef := config.FirestoreClient.Collection("teams").Doc(teamID)

	err = config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		_, err := tx.Get(teamRef)
		if err != nil {
			return &httpError{"Team not found", http.StatusNotFound}
		}

		updates := []firestore.Update{
			{Path: "Track", Value: payload.Track},
			{Path: "Subtrack", Value: payload.Subtrack},
			{Path: "UpdatedAt", Value: time.Now()},
		}

		return tx.Update(teamRef, updates)
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Track updated successfully"})
}

func SubmitProject(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	var payload SubmissionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if payload.ProjectDesc == nil || *payload.ProjectDesc == "" || payload.GithubLink == nil || *payload.GithubLink == "" {
		http.Error(w, "Project description and GitHub link are required", http.StatusBadRequest)
		return
	}

	teamID, err := verifyTeamLeader(ctx, r)
	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	teamRef := config.FirestoreClient.Collection("teams").Doc(teamID)
	err = config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(teamRef)
		if err != nil {
			return &httpError{"Team not found", http.StatusNotFound}
		}

		membersData, _ := doc.DataAt("members")
		if members, ok := membersData.([]interface{}); !ok || len(members) < minTeamSize {
			return &httpError{fmt.Sprintf("Team must have at least %d members to submit", minTeamSize), http.StatusForbidden}
		}

		now := time.Now()
		updates := []firestore.Update{
			{Path: "ProjectDesc", Value: payload.ProjectDesc},
			{Path: "GithubLink", Value: payload.GithubLink},
			{Path: "FigmaLink", Value: payload.FigmaLink},
			{Path: "OtherFiles", Value: payload.OtherFiles},
			{Path: "UpdatedAt", Value: now},
		}

		if payload.Track != nil {
			updates = append(updates, firestore.Update{Path: "Track", Value: payload.Track})
		}
		if payload.Subtrack != nil {
            updates = append(updates, firestore.Update{Path: "Subtrack", Value: payload.Subtrack})
		}

		if _, err := doc.DataAt("SubmittedAt"); err != nil {
			updates = append(updates, firestore.Update{Path: "SubmittedAt", Value: now})
		}

		return tx.Update(teamRef, updates)
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Project submitted successfully"})
}

func UpdateProject(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()

	var payload SubmissionPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	teamID, err := verifyTeamLeader(ctx, r)
	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	teamRef := config.FirestoreClient.Collection("teams").Doc(teamID)

	err = config.FirestoreClient.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(teamRef)
		if err != nil {
			return &httpError{"Team not found", http.StatusNotFound}
		}
		if _, err := doc.DataAt("SubmittedAt"); err != nil {
			return &httpError{"Project has not been submitted yet. Use the submit endpoint first.", http.StatusForbidden}
		}

		updates := []firestore.Update{}
		if payload.ProjectDesc != nil {
			updates = append(updates, firestore.Update{Path: "ProjectDesc", Value: *payload.ProjectDesc})
		}
		if payload.GithubLink != nil {
			updates = append(updates, firestore.Update{Path: "GithubLink", Value: *payload.GithubLink})
		}
		if payload.FigmaLink != nil {
			updates = append(updates, firestore.Update{Path: "FigmaLink", Value: *payload.FigmaLink})
		}
		if payload.Track != nil {
			updates = append(updates, firestore.Update{Path: "Track", Value: payload.Track})
		}
		if payload.Subtrack != nil {
			updates = append(updates, firestore.Update{Path: "Subtrack", Value: payload.Subtrack})
		}
		updates = append(updates, firestore.Update{Path: "OtherFiles", Value: payload.OtherFiles})

		if len(updates) == 0 {
			return &httpError{"No update data provided", http.StatusBadRequest}
		}

		updates = append(updates, firestore.Update{Path: "UpdatedAt", Value: time.Now()})

		return tx.Update(teamRef, updates)
	})

	if err != nil {
		handleFirestoreError(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Project updated successfully"})
}