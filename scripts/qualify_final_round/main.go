// qualify_final_round/main.go
//
// Usage (run from scripts/qualify_final_round/):
//   go run main.go -csv qualified_teams.csv
//
// CSV format: first row is a header (skipped), first column of each row is the team document ID.
//
// Behaviour:
//   - Reads all team documents from the "teams" Firestore collection.
//   - Sets isQualifiedForFinalRound = true  for teams whose ID appears in the CSV.
//   - Sets isQualifiedForFinalRound = false for all other teams.
//   - Uses MergeAll so only this field is touched; no other fields are overwritten.

package main

import (
	"context"
	"encoding/csv"
	"flag"
	"io"
	"log"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

func main() {
	csvPath := flag.String("csv", "qualified_teams.csv", "Path to CSV with qualified team IDs (first column, has header row)")
	flag.Parse()

	// ── 1. Load qualified IDs ─────────────────────────────────────────────────
	qualifiedIDs := loadTeamIDsFromCSV(*csvPath)
	log.Printf("[qualify_final_round] Loaded %d qualified team ID(s) from %q", len(qualifiedIDs), *csvPath)

	// ── 2. Init Firebase ──────────────────────────────────────────────────────
	opt := option.WithCredentialsFile("../../serviceAccountKey.json")
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		log.Fatalf("error initialising Firebase: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalf("error getting Firestore client: %v", err)
	}
	defer client.Close()

	// ── 3. Iterate all teams and batch-update ─────────────────────────────────
	iter := client.Collection("teams").Documents(ctx)
	defer iter.Stop()

	batch := client.Batch()
	batchCount := 0
	totalUpdated := 0
	qualified := 0
	notQualified := 0

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("error iterating teams: %v", err)
		}

		isQualified := qualifiedIDs[doc.Ref.ID]
		batch.Set(doc.Ref,
			map[string]interface{}{"isQualifiedForFinalRound": isQualified},
			firestore.MergeAll,
		)

		batchCount++
		totalUpdated++
		if isQualified {
			qualified++
			log.Printf("  ✓ %s → true", doc.Ref.ID)
		} else {
			notQualified++
		}

		if batchCount == 500 {
			commitBatch(ctx, batch)
			batch = client.Batch()
			batchCount = 0
			log.Printf("[qualify_final_round] Committed intermediate batch (500 writes)...")
		}
	}

	if batchCount > 0 {
		commitBatch(ctx, batch)
	}

	log.Printf("[qualify_final_round] Done. %d total teams updated — %d true, %d false.",
		totalUpdated, qualified, notQualified)
}

func commitBatch(ctx context.Context, batch *firestore.WriteBatch) {
	if _, err := batch.Commit(ctx); err != nil {
		log.Fatalf("error committing Firestore batch: %v", err)
	}
}

// loadTeamIDsFromCSV returns a set of team IDs from the first column (skips header).
func loadTeamIDsFromCSV(path string) map[string]bool {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("cannot open CSV %q: %v", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	ids := make(map[string]bool)
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
		if len(record) > 0 && record[0] != "" {
			ids[record[0]] = true
		}
	}
	return ids
}


// # final round qualification
// cd scripts/qualify_final_round && go run main.go -csv final_round.csv


