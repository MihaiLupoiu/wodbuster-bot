package wodbuster

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewClient(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	t.Run("with valid URL", func(t *testing.T) {
		client, err := NewClient(testBaseURL, WithLogger(logger))
		assert.NoError(t, err)
		assert.NotNil(t, client)
		client.Close()
	})

	t.Run("with empty URL", func(t *testing.T) {
		client, err := NewClient("")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingBaseURL)
		assert.Nil(t, client)
	})
}

func TestNewClientWithOptions(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		opts    []Option
		wantErr bool
	}{
		{
			name:    "with custom context",
			baseURL: testBaseURL,
			opts: []Option{
				WithContext(context.Background()),
				WithLogger(slog.Default()),
			},
			wantErr: false,
		},
		{
			name:    "with custom logger",
			baseURL: testBaseURL,
			opts: []Option{
				WithLogger(slog.Default()),
			},
			wantErr: false,
		},
		{
			name:    "with empty baseURL",
			baseURL: "",
			opts:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.baseURL, tt.opts...)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, client)
			assert.NotNil(t, client.ctx)
			assert.NotNil(t, client.cancel)
			assert.NotNil(t, client.logger)
			client.Close()
		})
	}
}
