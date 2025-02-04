package models

type UserSession struct {
	IsAuthenticated bool
	Username       string
	Token          string
}

type ClassSchedule struct {
	ID        string
	Day       string    // e.g., "Monday", "Tuesday"
	Hour      string    // e.g., "10:00"
	Available bool
	BookedBy  string    // Username of the person who booked
}
