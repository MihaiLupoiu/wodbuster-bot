package models

type UserSession struct {
	IsAuthenticated bool
	Username       string
	Token          string
}

type ClassSchedule struct {
	ID        string
	DateTime  string
	Available bool
}
