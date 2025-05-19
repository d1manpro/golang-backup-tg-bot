package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"gopkg.in/yaml.v3"
)

type Config struct {
	BotToken       string         `yaml:"token"`
	Bot            Bot            `yaml:"bot"`
	Archive        Archive        `yaml:"archive"`
	IncludeData    IncludeData    `yaml:"include_data"`
	Admins         []int64        `yaml:"admins"`
	BackupChatData BackupChatData `yaml:"backup_chat_data"`
	ClientApiData  ClientApiData  `yaml:"client_api_data"`
	DatabaseData   DatabaseData   `yaml:"database_data"`
}

type Bot struct {
	SendArchiveOnStart bool `yaml:"send_archive_on_start"`
	BotPolling         bool `yaml:"polling"`
}

type Archive struct {
	ExcludeFiles  []string     `yaml:"exclude_files"`
	ExcludeDirs   []string     `yaml:"exclude_dirs"`
	ArchiveName   string       `yaml:"archive_name"`
	TempBackupDir string       `yaml:"temp_backup_dir"`
	Timezone      string       `yaml:"timezone"`
	BackupTime    []BackupTime `yaml:"backup_time"`
}

type BackupTime struct {
	Hour   int `yaml:"hour"`
	Minute int `yaml:"minute"`
}

type IncludeData struct {
	Dirs      []PathMap `yaml:"dirs"`
	Files     []PathMap `yaml:"files"`
	Databases []string  `yaml:"databases"`
}

type PathMap struct {
	Path        string `yaml:"path"`
	ArchivePath string `yaml:"archive_path"`
}

type BackupChatData struct {
	ID       int64 `yaml:"id"`
	ThreadID int   `yaml:"thread_id"`
}

type ClientApiData struct {
	ID   int    `yaml:"id"`
	Hash string `yaml:"hash"`
}

type DatabaseData struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

func main() {
	data, err := os.ReadFile("backup_config.yml")
	if err != nil {
		panic(err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		panic(err)
	}

	ctx := context.Background()
	bot, err := telego.NewBot(cfg.BotToken)
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
			)).WithParseMode("HTML").WithMessageThreadID(update.Message.MessageThreadID),
		)
		log.Printf(
			"The /start command has been successfully processed. ChatID %d</code>, ThreadID %d</code>",
			update.Message.Chat.ID,
			update.Message.MessageThreadID,
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
