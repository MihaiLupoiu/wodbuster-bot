package handlers

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

//go:generate mockery --name=Bot --filename=mock_bot.go --inpackage --with-expecter=true --log-level=error
type Bot interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
}
