package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Ошибка загрузки .env файла")
	}

	botToken := os.Getenv("TOKEN")
	ctx := context.Background()

	bot, err := telego.NewBot(botToken)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	updates, _ := bot.UpdatesViaLongPolling(ctx, nil)

	log.Println("Polling started")
	_, _ = bot.SendMessage(ctx, tu.Message(tu.ID(5259467241), "Polling started"))

	bh, _ := th.NewBotHandler(bot, updates)

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		_, _ = bot.SendMessage(ctx, tu.Message(
			tu.ID(update.Message.Chat.ID),
			fmt.Sprintf(
				"The /start command has been successfully processed. ChatID <code>%d</code>, ThreadID <code>%d</code>",
				update.Message.Chat.ID,
				update.Message.MessageThreadID,
			)).WithParseMode("HTML"),
		)
		return nil
	}, th.CommandEqual("start"))

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		_, _ = bot.SendMessage(ctx, tu.Message(
			tu.ID(update.Message.Chat.ID),
			"Unknown command, use /start",
		))
		return nil
	}, th.AnyCommand())

	defer func() { _ = bh.Stop() }()

	_ = bh.Start()
}
