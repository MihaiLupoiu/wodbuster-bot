package models

type User struct {
	ChatID                int64                  `json:"chat_id" bson:"chat_id"`
	IsAuthenticated       bool                   `json:"is_authenticated" bson:"is_authenticated"`
	Email                 string                 `json:"email" bson:"email"`
	Password              string                 `json:"password" bson:"password"`
	Cookie                string                 `json:"cookie" bson:"cookie"`
	ClassBookingSchedules []ClassBookingSchedule `json:"class_booking_schedules" bson:"class_booking_schedules"`
}

type ClassBookingSchedule struct {
	ID        string `json:"id" bson:"id"`                 // Unique identifier for the class booking
	ClassType string `json:"class_type" bson:"class_type"` // e.g., "WOD", "Open"
	Day       string `json:"day" bson:"day"`               // e.g., "Monday", "Tuesday"
	Hour      string `json:"hour" bson:"hour"`             // e.g., "10:00"
}
