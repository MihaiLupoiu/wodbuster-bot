package models

type UserSession struct {
	ChatID          int64
	IsAuthenticated bool
	Username        string
	Password        string
	Token           string
}

type ClassSchedule struct {
	ID        string
	Day       string // e.g., "Monday", "Tuesday"
	Hour      string // e.g., "10:00"
	Available bool
	BookedBy  string // Username of the person who booked
}
