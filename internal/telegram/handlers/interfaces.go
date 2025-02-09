package handlers

type WodbusterClient interface {
    Login(username, password string) error
    BookClass(day, hour string) error
    RemoveBooking(day, hour string) error
}

type Logger interface {
    Error(msg string, args ...interface{})
}

type BotAPI interface {
    Send(c interface{}) (interface{}, error)
}
