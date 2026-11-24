package models

import (
	"cmp"
	"slices"
	"time"
)

type User struct {
	ChatID                int64                  `json:"chat_id" bson:"chat_id"`
	IsAuthenticated       bool                   `json:"is_authenticated" bson:"is_authenticated"`
	Email                 string                 `json:"email" bson:"email"`
	Password              string                 `json:"password" bson:"password"`
	ClassBookingSchedules []ClassBookingSchedule `json:"class_booking_schedules" bson:"class_booking_schedules"`

	// Role and Status decide who may use the bot. A newcomer's /login is
	// checked and saved, but the account stays pending until an admin approves
	// it: the run books only for active accounts. Documents from before these
	// fields existed are made active users at startup (Manager.Bootstrap).
	Role   string `json:"role" bson:"role"`
	Status string `json:"status" bson:"status"`

	// The WodBuster session is not kept here but in the sessions collection
	// (StoredSession), refreshed every 12 hours. The (encrypted) password is
	// still kept: the browser login is the fallback whenever a stored session
	// stops working.
	LastLoginTime time.Time `json:"last_login_time,omitempty" bson:"last_login_time,omitempty"`
	CreatedAt     time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" bson:"updated_at"`
}

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

const (
	StatusPending  = "pending"  // logged in, waiting for an admin
	StatusActive   = "active"   // booked for on Sundays, may use every command
	StatusDisabled = "disabled" // switched off by an admin; nothing is booked
)

// ValidStatus is whether s is one of the statuses an admin can set.
func ValidStatus(s string) bool {
	return s == StatusPending || s == StatusActive || s == StatusDisabled
}

func (u User) IsActive() bool { return u.Status == StatusActive }

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// ActiveAdmins are the users who hear about failures and new logins, in chat
// order so that "the first admin" is the same one every time. None is a valid
// answer: then nobody is told.
func ActiveAdmins(users []User) []User {
	var out []User
	for _, u := range users {
		if u.IsAdmin() && u.IsActive() {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b User) int { return cmp.Compare(a.ChatID, b.ChatID) })
	return out
}

type ClassBookingSchedule struct {
	ID        string `json:"id" bson:"id"`                 // Unique identifier for the class booking
	ClassType string `json:"class_type" bson:"class_type"` // e.g., "WOD", "Open"
	Day       string `json:"day" bson:"day"`               // e.g., "Monday", "Tuesday"
	Hour      string `json:"hour" bson:"hour"`             // e.g., "10:00"
}

// BookingAttempt tracks booking attempts - NO sensitive data stored here
// Use ChatID to lookup user credentials from User model when needed
type BookingAttempt struct {
	ID          string    `bson:"_id" json:"id"`
	ChatID      int64     `bson:"chat_id" json:"chat_id"` // Only reference to user
	Day         string    `bson:"day" json:"day"`
	Hour        string    `bson:"hour" json:"hour"`
	ClassType   string    `bson:"class_type" json:"class_type"`
	Status      string    `bson:"status" json:"status"` // pending, success, failed, expired
	AttemptTime time.Time `bson:"attempt_time" json:"attempt_time"`
	ErrorMsg    string    `bson:"error_msg,omitempty" json:"error_msg,omitempty"`
	RetryCount  int       `bson:"retry_count" json:"retry_count"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`
}

// BookingWindow represents when booking becomes available
type BookingWindow struct {
	Day           string        `json:"day"`
	Hour          string        `json:"hour"`
	ClassType     string        `json:"class_type"`
	OpensAt       time.Time     `json:"opens_at"`       // When booking opens
	TimeRemaining time.Duration `json:"time_remaining"` // Time until booking opens
	IsOpen        bool          `json:"is_open"`        // Whether booking is currently open
}
