package storage

import (
	"context"
	"time"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoStorage struct {
	client     *mongo.Client
	database   string
	sessions   *mongo.Collection
	classes    *mongo.Collection
	ctxTimeout time.Duration
}

type MongoOption func(*MongoStorage)

func WithClient(client *mongo.Client) MongoOption {
	return func(m *MongoStorage) {
		m.client = client
	}
}

func WithTimeout(timeout time.Duration) MongoOption {
	return func(m *MongoStorage) {
		m.ctxTimeout = timeout
	}
}

func NewMongoStorage(uri, database string, opts ...MongoOption) (*MongoStorage, error) {
	s := &MongoStorage{
		database:   database,
		ctxTimeout: 5 * time.Second,
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
	s.sessions = db.Collection("sessions")
	s.classes = db.Collection("classes")

	return s, nil
}

func (s *MongoStorage) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.ctxTimeout)
	defer cancel()
	return s.client.Disconnect(ctx)
}

func (s *MongoStorage) SaveSession(chatID int64, session models.UserSession) {
	ctx, cancel := context.WithTimeout(context.Background(), s.ctxTimeout)
	defer cancel()

	filter := bson.M{"chatID": chatID}
	update := bson.M{"$set": session}
	opts := options.Update().SetUpsert(true)

	_, err := s.sessions.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		// In a production environment, you might want to handle this error more gracefully
		panic(err)
	}
}

func (s *MongoStorage) GetSession(chatID int64) (models.UserSession, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), s.ctxTimeout)
	defer cancel()

	var session models.UserSession
	err := s.sessions.FindOne(ctx, bson.M{"chatID": chatID}).Decode(&session)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return models.UserSession{}, false
		}
		panic(err)
	}

	return session, true
}

func (s *MongoStorage) SaveClass(class models.ClassSchedule) {
	ctx, cancel := context.WithTimeout(context.Background(), s.ctxTimeout)
	defer cancel()

	filter := bson.M{"_id": class.ID}
	update := bson.M{"$set": class}
	opts := options.Update().SetUpsert(true)

	_, err := s.classes.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		panic(err)
	}
}

func (s *MongoStorage) GetClass(classID string) (models.ClassSchedule, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), s.ctxTimeout)
	defer cancel()

	var class models.ClassSchedule
	err := s.classes.FindOne(ctx, bson.M{"_id": classID}).Decode(&class)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return models.ClassSchedule{}, false
		}
		panic(err)
	}

	return class, true
}

func (s *MongoStorage) GetAllClasses() []models.ClassSchedule {
	ctx, cancel := context.WithTimeout(context.Background(), s.ctxTimeout)
	defer cancel()

	cursor, err := s.classes.Find(ctx, bson.M{})
	if err != nil {
		panic(err)
	}
	defer cursor.Close(ctx)

	var classes []models.ClassSchedule
	if err := cursor.All(ctx, &classes); err != nil {
		panic(err)
	}

	return classes
}
