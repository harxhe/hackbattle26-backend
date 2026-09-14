package main

import (
	"context"
	"encoding/csv"
	"flag"
	"io"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/option"
)

type CsvRow struct {
	RegNo    string
	Name     string
	Email    string
	TeamName string
}

func main() {
	csvPath := flag.String("csv", "users.csv", "Path to CSV file with reg_no, name, email, team_name")
	flag.Parse()

	// ── 1. Read CSV ───────────────────────────────────────────────────────────
	rows := readCSV(*csvPath)
	log.Printf("Loaded %d rows from %s", len(rows), *csvPath)

	// Group users by team_name
	teamGroups := make(map[string][]CsvRow)
	for _, row := range rows {
		teamGroups[row.TeamName] = append(teamGroups[row.TeamName], row)
	}

	// ── 2. Init Firebase ──────────────────────────────────────────────────────
	opt := option.WithCredentialsFile("../../serviceAccountKey.json")
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		log.Fatalf("error initializing Firebase: %v", err)
	}

	ctx := context.Background()
	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("error getting Firestore client: %v", err)
	}
	defer client.Close()

	// ── 3. Process Each Team Group ────────────────────────────────────────────
	teamsCollection := client.Collection("teams")
	usersCollection := client.Collection("users")

	stats := struct {
		TeamsCreated int
		TeamsExisted int
		UsersAdded   int
		UsersExisted int
	}{}

	for teamName, members := range teamGroups {
		log.Printf("Processing team: %q (%d members)", teamName, len(members))

		// Check if team exists
		q := teamsCollection.Where("Name", "==", teamName).Limit(1)
		docs, err := q.Documents(ctx).GetAll()
		if err != nil {
			log.Fatalf("error querying team %q: %v", teamName, err)
		}

		var teamCode string
		teamExists := len(docs) > 0

		if teamExists {
			teamCode = docs[0].Ref.ID
			stats.TeamsExisted++
			log.Printf("  Team exists with code: %s", teamCode)
		} else {
			// Generate new team code
			teamCode = generateTeamCode(ctx, teamsCollection)
			stats.TeamsCreated++
			log.Printf("  Team does not exist. Created new code: %s", teamCode)

			leaderEmail := strings.ToLower(members[0].Email)
			
			_, err = teamsCollection.Doc(teamCode).Set(ctx, map[string]interface{}{
				"Name":      teamName,
				"Code":      teamCode,
				"leaderId":  leaderEmail,
				"members":   []map[string]interface{}{},
				"CreatedAt": time.Now(),
			})
			if err != nil {
				log.Fatalf("error creating team document %q: %v", teamCode, err)
			}
		}

		// Process each member
		for i, member := range members {
			emailLower := strings.ToLower(member.Email)
			isLead := false
			
			// If it's a new team, the first user in CSV group is leader.
			if !teamExists && i == 0 {
				isLead = true
			}

			isVITian := strings.Contains(emailLower, "vitstudent.ac.in")

			// Check if user already exists
			userDocRef := usersCollection.Doc(emailLower)
			userDoc, err := userDocRef.Get(ctx)
			if err != nil && strings.Contains(err.Error(), "NotFound") {
				// Handle different gRPC NotFound error if any
			}
			userExists := userDoc != nil && userDoc.Exists()

			if userExists {
				stats.UsersExisted++
			} else {
				stats.UsersAdded++
			}

			// Add to team's members array
			memberMap := map[string]interface{}{
				"email": emailLower,
				"name":  member.Name,
			}
			_, err = teamsCollection.Doc(teamCode).Update(ctx, []firestore.Update{
				{Path: "members", Value: firestore.ArrayUnion(memberMap)},
			})
			if err != nil {
				log.Fatalf("error adding member to team %q: %v", teamCode, err)
			}

			// Upsert user document
			userUpdates := map[string]interface{}{
				"name":      member.Name,
				"email":     emailLower,
				"regNo":     member.RegNo,
				"TeamID":    teamCode,
				"isVITian":  isVITian,
			}
			
			if isLead {
				userUpdates["IsLead"] = true
			}

			_, err = userDocRef.Set(ctx, userUpdates, firestore.MergeAll)
			if err != nil {
				log.Fatalf("error updating user %q: %v", emailLower, err)
			}
			
			log.Printf("    Added/Updated user %q to team %q", emailLower, teamCode)
		}
	}

	log.Println("──────────────────────────────────────────")
	log.Println("Seeding complete! Statistics:")
	log.Printf("  Total rows in CSV  : %d", len(rows))
	log.Printf("  New Teams Created  : %d", stats.TeamsCreated)
	log.Printf("  Teams Already Exist: %d", stats.TeamsExisted)
	log.Printf("  New Users Added    : %d", stats.UsersAdded)
	log.Printf("  Users Already Exist: %d", stats.UsersExisted)
	log.Println("──────────────────────────────────────────")
}

func readCSV(path string) []CsvRow {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("error opening CSV %q: %v", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	var rows []CsvRow
	header := true

	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("error reading CSV: %v", err)
		}
		if header {
			header = false
			continue
		}
		if len(record) >= 4 {
			rows = append(rows, CsvRow{
				RegNo:    strings.TrimSpace(record[0]),
				Name:     strings.TrimSpace(record[1]),
				Email:    strings.TrimSpace(record[2]),
				TeamName: strings.TrimSpace(record[3]),
			})
		}
	}
	return rows
}

func generateTeamCode(ctx context.Context, teamsCollection *firestore.CollectionRef) string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rand.Seed(time.Now().UnixNano())
	
	for {
		b := make([]byte, 6)
		for i := range b {
			b[i] = letters[rand.Intn(len(letters))]
		}
		code := string(b)
		
		doc, err := teamsCollection.Doc(code).Get(ctx)
		if err != nil {
			return code // Assume not found
		}
		if !doc.Exists() {
			return code
		}
	}
}
