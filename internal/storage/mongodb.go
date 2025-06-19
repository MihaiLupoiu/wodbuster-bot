package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoStorage struct {
	client   *mongo.Client
	database string
	users    *mongo.Collection
}

type MongoOption func(*MongoStorage)

func WithClient(client *mongo.Client) MongoOption {
	return func(m *MongoStorage) {
		m.client = client
	}
}

func NewMongoStorage(uri, database string, opts ...MongoOption) (*MongoStorage, error) {
	s := &MongoStorage{
		database: database,
	}

	// Apply options
	for _, opt := range opts {
		opt(s)
	}

	// If no client provided, create one
	if s.client == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
		if err != nil {
			return nil, err
		}

		// Ping the database to verify connection
		if err := client.Ping(ctx, nil); err != nil {
			return nil, err
		}

		s.client = client
	}

	db := s.client.Database(s.database)
	s.users = db.Collection("users")

	return s, nil
}

func (s *MongoStorage) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

func (s *MongoStorage) SaveUser(ctx context.Context, user models.User) error {
	filter := bson.M{"chatID": user.ChatID}
	update := bson.M{"$set": user}
	opts := options.Update().SetUpsert(true)

	_, err := s.users.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return fmt.Errorf("error saving user %v: %w", user, err)
	}
	return nil
}

func (s *MongoStorage) GetUser(ctx context.Context, chatID int64) (models.User, bool) {
	var session models.User
	err := s.users.FindOne(ctx, bson.M{"chatID": chatID}).Decode(&session)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return models.User{}, false
		}
		panic(err)
	}

	return session, true
}

func (s *MongoStorage) SaveClassBookingSchedule(ctx context.Context, chatID int64, class models.ClassBookingSchedule) error {
	user, exists := s.GetUser(ctx, chatID)
	if !exists {
		return fmt.Errorf("user with chatID %d does not exist", chatID)
	}
	user.ClassBookingSchedules = append(user.ClassBookingSchedules, class)

	return s.SaveUser(ctx, user)
}

func (s *MongoStorage) GetClassBookingSchedules(ctx context.Context, chatID int64) ([]models.ClassBookingSchedule, bool) {
	user, exists := s.GetUser(ctx, chatID)
	if !exists {
		return nil, false
	}

	return user.ClassBookingSchedules, true
}
