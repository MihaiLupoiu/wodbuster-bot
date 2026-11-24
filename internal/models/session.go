package models

import "time"

// StoredSession is an athlete's WodBuster session, kept between runs so the
// Sunday booking can start from a login made earlier instead of one made
// minutes before the opening.
//
// It lives in its own collection, keyed by chat id, not on User: SaveUser
// replaces the whole user document, a refresh every 12 hours should not
// rewrite profiles, and the credential stays apart from them.
//
// Encrypted is the whole wodbuster.Session as JSON, sealed with the same key
// as the password. It is a bearer credential: never logged, never stored in
// plain text. The other fields are metadata, plain so that /status and the
// metrics can read them without the key.
type StoredSession struct {
	ChatID    int64  `json:"chat_id" bson:"chat_id"`
	Encrypted string `json:"-" bson:"encrypted"`

	// LoggedInAt is the browser login that minted the session. Its age is
	// how long sessions really last.
	LoggedInAt time.Time `json:"logged_in_at" bson:"logged_in_at"`
	// LastUsedAt is the last request WodBuster accepted with it. The gap
	// since then is the idle time a session has to survive.
	LastUsedAt time.Time `json:"last_used_at" bson:"last_used_at"`
	// AuthExpiresAt is the Expires of .WBAuth, zero for a session cookie.
	AuthExpiresAt time.Time `json:"auth_expires_at,omitempty" bson:"auth_expires_at,omitempty"`
	// TrustedDevice is whether .WBAuth has an expiry, which is what
	// answering "remember this device" gives it. Inferred from the cookie,
	// not recorded at the login.
	TrustedDevice bool      `json:"trusted_device" bson:"trusted_device"`
	UpdatedAt     time.Time `json:"updated_at" bson:"updated_at"`
}
