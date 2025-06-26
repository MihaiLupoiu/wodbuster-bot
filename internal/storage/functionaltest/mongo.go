package functionaltest

import (
	"context"
	"fmt"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDBContainer struct {
	URI    string
	DBName string
}

// CreateMongoContainer creates a MongoDB test container with a unique database
func CreateMongoContainer(ctx context.Context, t *testing.T, dbName string) (*mongo.Client, string, error) {
	req := testcontainers.ContainerRequest{
		Image:        "mongo:latest",
		ExposedPorts: []string{"27017/tcp"},
		WaitingFor:   wait.ForLog("Waiting for connections"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to start container: %v", err)
	}

	mappedPort, err := container.MappedPort(ctx, "27017")
	if err != nil {
		return nil, "", fmt.Errorf("failed to get container external port: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get container host: %v", err)
	}

	uri := fmt.Sprintf("mongodb://%s:%s", host, mappedPort.Port())

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, "", fmt.Errorf("failed to connect to mongodb: %v", err)
	}

	// Clean up both the container and the test database
	t.Cleanup(func() {
		if err := mongoClient.Database(dbName).Drop(ctx); err != nil {
			t.Logf("failed to drop database %s: %v", dbName, err)
		}
		if err := container.Terminate(ctx); err != nil {
			t.Fatalf("failed to terminate container: %v", err)
		}
	})

	return mongoClient, uri, nil
}
