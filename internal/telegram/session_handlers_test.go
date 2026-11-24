package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/MihaiLupoiu/wodbuster-bot/internal/models"
)

// fakeTelegram answers every Bot API call with success and keeps the texts
// the bot sent, so a handler's reply can be read back.
type fakeTelegram struct {
	mu    sync.Mutex
	texts []string
}

func (f *fakeTelegram) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.texts = append(f.texts, form.Get("text"))
	f.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":1,"chat":{"id":1}}}`)),
	}, nil
}

func (f *fakeTelegram) sent() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.texts, "\n---\n")
}

func testBot(t *testing.T, manager BotManager) (*Bot, *fakeTelegram) {
	t.Helper()
	tg := &fakeTelegram{}
	api := &tgbotapi.BotAPI{Token: "test", Client: tg}
	api.SetAPIEndpoint(tgbotapi.APIEndpoint)
	return &Bot{
		api:     api,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		manager: manager,
	}, tg
}

func TestUsersMarksOnlyActiveUsersKnownToHaveNoStoredSession(t *testing.T) {
	m := NewMockBotManager(t)
	m.EXPECT().ListUsers(mock.Anything).Return([]models.User{
		{ChatID: 1, Email: "unreadable@example.com", Status: models.StatusActive},
		{ChatID: 2, Email: "off@example.com", Status: models.StatusDisabled},
		{ChatID: 3, Email: "none@example.com", Status: models.StatusActive},
	}, nil)
	m.EXPECT().GetSession(mock.Anything, int64(1)).Return(models.StoredSession{}, false, errors.New("mongo down"))
	m.EXPECT().GetSession(mock.Anything, int64(3)).Return(models.StoredSession{}, false, nil)
	// No expectation for chat 2: the mock fails the test if a disabled
	// user's session is read.

	b, tg := testBot(t, m)
	b.handleUsers(context.Background(), 99)

	got := tg.sent()
	assert.Contains(t, got, "✅ unreadable@example.com · chat 1 · 0 class(es)\n",
		"a store error is not reported as a missing session")
	assert.Contains(t, got, "⛔ off@example.com · chat 2 · 0 class(es)\n")
	assert.Contains(t, got, "✅ none@example.com · chat 3 · 0 class(es) · ⚠️ no stored session")
}

func TestUsersReadsSessionsWithADeadline(t *testing.T) {
	m := NewMockBotManager(t)
	m.EXPECT().ListUsers(mock.Anything).Return([]models.User{{ChatID: 1, Status: models.StatusActive}}, nil)
	m.EXPECT().GetSession(mock.Anything, int64(1)).
		RunAndReturn(func(ctx context.Context, _ int64) (models.StoredSession, bool, error) {
			deadline, ok := ctx.Deadline()
			assert.True(t, ok, "GetSession needs a deadline")
			assert.WithinDuration(t, time.Now().Add(storeTimeout), deadline, time.Second)
			return models.StoredSession{}, true, nil
		})

	b, _ := testBot(t, m)
	b.handleUsers(context.Background(), 99)
}

func TestStatusSaysWhenTheSessionCannotBeRead(t *testing.T) {
	m := NewMockBotManager(t)
	m.EXPECT().GetUser(mock.Anything, int64(7)).
		Return(models.User{ChatID: 7, Email: "a@example.com", Status: models.StatusActive}, true)
	m.EXPECT().GetSession(mock.Anything, int64(7)).
		RunAndReturn(func(ctx context.Context, _ int64) (models.StoredSession, bool, error) {
			_, ok := ctx.Deadline()
			assert.True(t, ok, "GetSession needs a deadline")
			return models.StoredSession{}, false, errors.New("mongo down")
		})

	b, tg := testBot(t, m)
	b.handleStatus(tgbotapi.Update{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 7}}})

	assert.Contains(t, tg.sent(), "WodBuster session: ❓ could not be read right now")
}

func TestStatusGivesTheExpiryInTheBotsLocation(t *testing.T) {
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)
	m := NewMockBotManager(t)
	m.EXPECT().GetUser(mock.Anything, int64(7)).
		Return(models.User{ChatID: 7, Email: "a@example.com", Status: models.StatusActive}, true)
	m.EXPECT().GetSession(mock.Anything, int64(7)).Return(models.StoredSession{
		LastUsedAt:    time.Now(),
		AuthExpiresAt: time.Date(2027, 1, 1, 23, 30, 0, 0, time.UTC),
	}, true, nil)

	b, tg := testBot(t, m)
	b = b.WithLocation(madrid)
	b.handleStatus(tgbotapi.Update{Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 7}}})

	assert.Contains(t, tg.sent(), "login cookie expires 2027-01-02")
}
