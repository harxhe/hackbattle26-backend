package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/option"
)

type User struct {
	Name               string    `json:"name" firestore:"name"`
	Email              string    `json:"email" firestore:"email"`
	TeamID             *string   `json:"teamId,omitempty" firestore:"TeamID,omitempty"`
	IsLead             bool      `json:"isLead" firestore:"IsLead"`
	IsVITian           bool      `json:"isVITian" firestore:"isVITian"`
	RegNo             string    `json:"regNo" firestore:"regNo"`
	CreatedAt          time.Time `json:"createdAt" firestore:"createdAt"`
}

func main() {
	// 1. Initialize Firebase
	opt := option.WithCredentialsFile("../serviceAccountKey.json") // Pointing to the root directory
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		log.Fatalf("error initializing app: %v\n", err)
	}

	client, err := app.Firestore(context.Background())
	if err != nil {
		log.Fatalf("error getting Firestore client: %v\n", err)
	}
	defer client.Close()

	// 2. Read the users from users.json
	file, err := os.ReadFile("users.json")
	if err != nil {
		log.Fatalf("error reading users.json: %v\n", err)
	}

	var users []User
	if err := json.Unmarshal(file, &users); err != nil {
		log.Fatalf("error parsing JSON: %v\n", err)
	}

	// 3. Insert users into Firestore
	// Use a timeout so it doesn't hang forever if there's a connection issue
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	
	batch := client.Batch()
	count := 0

	for _, user := range users {
		// Use lowercased email as the Document ID (authController.go expects this)
		docID := strings.ToLower(user.Email)

		// Set default values
		user.Email = docID
		user.CreatedAt = time.Now()
		user.IsLead = false
		user.TeamID = nil

		docRef := client.Collection("users").Doc(docID)

		batch.Set(docRef, user)
		count++

		// Firestore batches can hold up to 500 writes
		if count == 500 {
			_, err := batch.Commit(ctx)
			if err != nil {
				log.Fatalf("error committing batch: %v\n", err)
			}
			batch = client.Batch()
			count = 0
		}
	}

	// Commit any remaining writes
	if count > 0 {
		_, err := batch.Commit(ctx)
		if err != nil {
			log.Fatalf("error committing final batch: %v\n", err)
		}
	}

	log.Printf("Successfully seeded %d users!\n", len(users))
}
