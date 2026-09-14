package models

import (
	"time"
)

type Team struct {
	ID          string     `json:"id" firestore:"-"`
	Name        string     `json:"name" firestore:"Name"`
	Code        string     `json:"code" firestore:"Code"`
	CreatedAt   time.Time  `json:"createdAt" firestore:"CreatedAt"`
	LeaderID    string     `json:"leader_id" firestore:"leaderId"`

	// Track
	Track *string `json:"track,omitempty" firestore:"Track,omitempty"`

	// Submission Details
	FigmaLink  *string  `json:"figma_link,omitempty" firestore:"FigmaLink,omitempty"`
	OtherLinks []string `json:"other_links,omitempty" firestore:"OtherLinks,omitempty"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty" firestore:"SubmittedAt,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty" firestore:"UpdatedAt,omitempty"`
}