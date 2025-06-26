package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoStorage struct {
	client             *mongo.Client
	database           *mongo.Database
	usersCollection    *mongo.Collection
	bookingsCollection *mongo.Collection
}

func NewMongoStorage(uri, dbName string) (*MongoStorage, error) {
	client, err := mongo.Connect(context.Background(), options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MongoDB: %w", err)
	}

	database := client.Database(dbName)
	usersCollection := database.Collection("users")
	bookingsCollection := database.Collection("booking_attempts")

	return &MongoStorage{
		client:             client,
		database:           database,
		usersCollection:    usersCollection,
		bookingsCollection: bookingsCollection,
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

func (m *MongoStorage) GetAllPendingBookings(ctx context.Context) ([]models.BookingAttempt, error) {
	cursor, err := m.bookingsCollection.Find(ctx, bson.M{"status": "pending"})
	if err != nil {
		return nil, fmt.Errorf("failed to get pending bookings: %w", err)
	}
	defer cursor.Close(ctx)

	var bookings []models.BookingAttempt
	if err := cursor.All(ctx, &bookings); err != nil {
		return nil, fmt.Errorf("failed to decode booking attempts: %w", err)
	}

	return bookings, nil
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
