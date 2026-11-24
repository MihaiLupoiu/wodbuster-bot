package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// connectTimeout bounds the initial connection. Long enough for a hosted
// cluster waking up, short enough that a misconfigured deployment says so
// rather than hanging.
const connectTimeout = 15 * time.Second

type MongoStorage struct {
	client             *mongo.Client
	database           *mongo.Database
	usersCollection    *mongo.Collection
	bookingsCollection *mongo.Collection
	sessionsCollection *mongo.Collection
}

// NewMongoStorage connects without metrics. Prefer NewMongoStorageWithMetrics.
func NewMongoStorage(uri, dbName string) (*MongoStorage, error) {
	return NewMongoStorageWithMetrics(uri, dbName, nil)
}

// NewMongoStorageWithMetrics connects and instruments the driver through its
// own event monitors, so every command is counted without wrapping any call.
func NewMongoStorageWithMetrics(uri, dbName string, m *MongoMetrics) (*MongoStorage, error) {
	// mongo.Connect does not talk to the server: it validates the URI and
	// returns. Against a container on the same network that hardly matters,
	// but against a hosted database — wrong password, an IP that is not on the
	// access list, a paused cluster — the bot would start up looking healthy
	// and only fail hours later, mid-booking. So connect, then prove it.
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	poolMonitor, cmdMonitor := m.Monitors()
	// v2's Connect takes no context: it does no I/O, which is exactly the
	// surprise the Ping below exists to cover.
	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(connectTimeout).
		SetPoolMonitor(poolMonitor).
		SetMonitor(cmdMonitor))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("could not reach MongoDB: %w "+
			"(check the credentials in MONGO_URI, and that this host's IP is "+
			"allowed to connect)", err)
	}

	database := client.Database(dbName)
	usersCollection := database.Collection("users")
	bookingsCollection := database.Collection("booking_attempts")
	sessionsCollection := database.Collection("sessions")

	// One session per athlete. Creating an index that already exists is a
	// no-op, so this is safe on every start.
	if _, err := sessionsCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "chat_id", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("chat_id_unique"),
	}); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("could not create the sessions index: %w", err)
	}

	return &MongoStorage{
		client:             client,
		database:           database,
		usersCollection:    usersCollection,
		bookingsCollection: bookingsCollection,
		sessionsCollection: sessionsCollection,
	}, nil
}

func (m *MongoStorage) Close() error {
	return m.client.Disconnect(context.Background())
}

func (m *MongoStorage) SaveUser(ctx context.Context, user models.User) error {
	user.UpdatedAt = time.Now()

	_, err := m.usersCollection.ReplaceOne(
		ctx,
		bson.M{"chat_id": user.ChatID},
		user,
		options.Replace().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("failed to save user: %w", err)
	}

	return nil
}

func (m *MongoStorage) GetUser(ctx context.Context, chatID int64) (models.User, bool) {
	var user models.User
	err := m.usersCollection.FindOne(ctx, bson.M{"chat_id": chatID}).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return models.User{}, false
		}
		return models.User{}, false
	}

	return user, true
}

func (m *MongoStorage) SaveClassBookingSchedule(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error {
	user, exists := m.GetUser(ctx, chatID)
	if !exists {
		return fmt.Errorf("user with chat ID %d not found", chatID)
	}

	// Check if class already exists and update it, otherwise append
	found := false
	for i, existingClass := range user.ClassBookingSchedules {
		if existingClass.ID == class.ID {
			user.ClassBookingSchedules[i] = class
			found = true
			break
		}
	}

	if !found {
		user.ClassBookingSchedules = append(user.ClassBookingSchedules, class)
	}

	return m.SaveUser(ctx, user)
}

func (m *MongoStorage) GetClassBookingSchedules(ctx context.Context, chatID int64) ([]models.ClassBookingSchedule, bool) {
	user, exists := m.GetUser(ctx, chatID)
	if !exists {
		return nil, false
	}

	return user.ClassBookingSchedules, true
}

// BookingAttempt methods
func (m *MongoStorage) SaveBookingAttempt(ctx context.Context, attempt models.BookingAttempt) error {
	attempt.UpdatedAt = time.Now()

	_, err := m.bookingsCollection.ReplaceOne(
		ctx,
		bson.M{"_id": attempt.ID},
		attempt,
		options.Replace().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("failed to save booking attempt: %w", err)
	}

	return nil
}

func (m *MongoStorage) ListUsers(ctx context.Context) ([]models.User, error) {
	cursor, err := m.usersCollection.Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer cursor.Close(ctx)

	var users []models.User
	if err := cursor.All(ctx, &users); err != nil {
		return nil, fmt.Errorf("failed to decode users: %w", err)
	}

	return users, nil
}

func (m *MongoStorage) UpdateBookingStatus(ctx context.Context, attemptID string, status string, errorMsg string) error {
	update := bson.M{
		"$set": bson.M{
			"status":     status,
			"error_msg":  errorMsg,
			"updated_at": time.Now(),
		},
	}

	_, err := m.bookingsCollection.UpdateOne(
		ctx,
		bson.M{"_id": attemptID},
		update,
	)
	if err != nil {
		return fmt.Errorf("failed to update booking status: %w", err)
	}

	return nil
}

// SaveSession stores the athlete's session, replacing any older one. Only the
// session document is written; the user is not touched.
func (m *MongoStorage) SaveSession(ctx context.Context, s models.StoredSession) error {
	s.UpdatedAt = sessionStamp()
	_, err := m.sessionsCollection.ReplaceOne(ctx,
		bson.M{"chat_id": s.ChatID}, s, options.Replace().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("failed to save session: %w", err)
	}
	return nil
}

// GetSession returns the athlete's stored session. A missing one is not an
// error: ok is false. An error means the database could not answer, which the
// caller must not mistake for "no session".
func (m *MongoStorage) GetSession(ctx context.Context, chatID int64) (models.StoredSession, bool, error) {
	var s models.StoredSession
	err := m.sessionsCollection.FindOne(ctx, bson.M{"chat_id": chatID}).Decode(&s)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.StoredSession{}, false, nil
	}
	if err != nil {
		return models.StoredSession{}, false, fmt.Errorf("failed to read session: %w", err)
	}
	return s, true, nil
}

// DeleteSession forgets the athlete's session. Deleting one that is not there
// is fine.
func (m *MongoStorage) DeleteSession(ctx context.Context, chatID int64) error {
	if _, err := m.sessionsCollection.DeleteOne(ctx, bson.M{"chat_id": chatID}); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// ReplaceSession stores s only if the stored session is still the one whose
// UpdatedAt was prevUpdatedAt. ok is false when it changed or is gone: someone
// else wrote it in between, and their write stands. prevUpdatedAt must come
// from GetSession, so it carries Mongo's millisecond precision.
func (m *MongoStorage) ReplaceSession(ctx context.Context, prevUpdatedAt time.Time, s models.StoredSession) (bool, error) {
	s.UpdatedAt = sessionStamp()
	res, err := m.sessionsCollection.ReplaceOne(ctx,
		bson.M{"chat_id": s.ChatID, "updated_at": prevUpdatedAt}, s)
	if err != nil {
		return false, fmt.Errorf("failed to replace session: %w", err)
	}
	return res.MatchedCount == 1, nil
}

// DeleteSessionIfUnchanged is DeleteSession with ReplaceSession's condition.
func (m *MongoStorage) DeleteSessionIfUnchanged(ctx context.Context, chatID int64, prevUpdatedAt time.Time) (bool, error) {
	res, err := m.sessionsCollection.DeleteOne(ctx, bson.M{"chat_id": chatID, "updated_at": prevUpdatedAt})
	if err != nil {
		return false, fmt.Errorf("failed to delete session: %w", err)
	}
	return res.DeletedCount == 1, nil
}
