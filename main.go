package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
)

// https://chatgpt.com/c/682c400b-3f7c-8012-bfbb-62d6cba1193f

type Config struct {
	Bot            Bot            `yaml:"bot"`
	Archive        Archive        `yaml:"archive"`
	IncludeData    IncludeData    `yaml:"include_data"`
	Admins         []int64        `yaml:"admins"`
	BackupChatData BackupChatData `yaml:"backup_chat_data"`
	ClientApiData  ClientApiData  `yaml:"client_api_data"`
	DatabaseData   DatabaseData   `yaml:"database_data"`
}

var cfg Config

type Bot struct {
	BotToken           string `yaml:"token"`
	SendArchiveOnStart bool   `yaml:"send_archive_on_start"`
}

type Archive struct {
	ExcludeFiles  []string `yaml:"exclude_files"`
	ExcludeDirs   []string `yaml:"exclude_dirs"`
	ArchiveName   string   `yaml:"archive_name"`
	TempBackupDir string   `yaml:"temp_backup_dir"`
	Timezone      string   `yaml:"timezone"`
	BackupTime    []string `yaml:"backup_time"`
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

func setupLogger() *zap.Logger {
	encoderCfg := zapcore.EncoderConfig{
		TimeKey:     "time",
		LevelKey:    "level",
		MessageKey:  "msg",
		EncodeTime:  zapcore.TimeEncoderOfLayout("[2006.01.02][15:04:05]"),
		EncodeLevel: zapcore.CapitalLevelEncoder,
	}

	encoder := zapcore.NewConsoleEncoder(encoderCfg)
	consoleCore := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zapcore.InfoLevel)

	logger := zap.New(consoleCore)
	return logger
}

var logger = setupLogger()

func main() {
	data, err := os.ReadFile("backup_config.yml")
	if err != nil {
		logger.Fatal("Failed to load config file", zap.Error(err))
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		logger.Fatal(fmt.Sprintf("%v", err))
	}

	ctx := context.Background()
	bot, err := telego.NewBot(cfg.Bot.BotToken)
	if err != nil {
		logger.Fatal("Failed to load bot", zap.Error(err))
	}

	if cfg.Bot.SendArchiveOnStart {
		sendBackup(bot, ctx, cfg.BackupChatData.ID, cfg.BackupChatData.ThreadID)
		logger.Info("Startup backup succesfully sended")
	}

	updates, _ := bot.UpdatesViaLongPolling(ctx, nil)
	logger.Info("Polling started")
	_, _ = bot.SendMessage(ctx, tu.Message(tu.ID(5259467241), "Polling started"))
	bh, _ := th.NewBotHandler(bot, updates)

	location, err := time.LoadLocation(cfg.Archive.Timezone)
	if err != nil {
		logger.Fatal("Failed to load timezone", zap.Error(err))
	}
	c := cron.New(cron.WithLocation(location))

	for _, t := range cfg.Archive.BackupTime {
		spec := t
		_, err := c.AddFunc(spec, func() {
			sendBackup(bot, ctx, cfg.BackupChatData.ID, cfg.BackupChatData.ThreadID)
			logger.Info("CronJob successfully done", zap.String("time", t))
		})
		if err != nil {
			logger.Error("Failed to add CronJob", zap.Error(err))
		} else {
			logger.Info("CronJob added", zap.String("time", t))
		}
	}
	c.Start()

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		_, _ = bot.SendMessage(ctx, tu.Message(
			tu.ID(update.Message.Chat.ID),
			fmt.Sprintf(
				"The /start command has been successfully processed. ChatID <code>%d</code>, ThreadID <code>%d</code>",
				update.Message.Chat.ID,
				update.Message.MessageThreadID,
			)).WithParseMode("HTML").WithMessageThreadID(update.Message.MessageThreadID),
		)
		logger.Info("The /start command has been successfully processed",
			zap.Int64("chat_id", update.Message.Chat.ID),
			zap.Int("thread_id", update.Message.MessageThreadID),
		)
		return nil
	}, th.CommandEqual("start"))

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		var message_chat_id int64
		var message_therad_id int
		if update.Message.Chat.ID != 0 {
			message_chat_id = update.Message.Chat.ID
			message_therad_id = update.Message.MessageThreadID
		} else {
			message_chat_id = cfg.BackupChatData.ID
			message_therad_id = cfg.BackupChatData.ThreadID
		}

		sendBackup(bot, ctx, message_chat_id, message_therad_id)

		return nil
	}, th.CommandEqual("backup"))

	defer func() { _ = bh.Stop() }()

	_ = bh.Start()
}

func sendBackup(bot *telego.Bot, ctx context.Context, message_chat_id int64, message_therad_id int) {
	archive_file, err := createArchive()
	if err != nil {
		logger.Error("Failed to open created archive", zap.Error(err))
	}

	_, _ = bot.SendMessage(ctx, tu.Message(
		tu.ID(message_chat_id),
		fmt.Sprintf(
			"The /backup command has been successfully processed. ChatID <code>%d</code>, ThreadID <code>%d</code>",
			message_chat_id,
			message_therad_id,
		)).WithParseMode("HTML").WithMessageThreadID(message_therad_id),
	)

	_, err = bot.SendDocument(ctx, tu.Document(
		tu.ID(message_chat_id),
		tu.File(archive_file),
	).WithMessageThreadID(message_therad_id))
	if err != nil {
		logger.Error("Failed to send archive", zap.Error(err))
	}
}

//func mustOpen(filename string) *os.File {
//	file, err := os.Open(filename)
//	if err != nil {
//		panic(err)
//	}
//	return file
//}

func createArchive() (*os.File, error) {
	outFile, err := os.Create("backup.tar.gz")
	if err != nil {
		logger.Error("Failed to create archive file", zap.Error(err))
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	for _, file := range cfg.IncludeData.Files {
		addFile(tw, file.Path, file.ArchivePath)
	}

	for _, dir := range cfg.IncludeData.Dirs {
		addDir(tw, dir.Path, dir.ArchivePath)
	}

	return os.Open(outFile.Name())
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return path // не удалось получить домашнюю директорию — вернём как есть
		}
		return strings.Replace(path, "~", homeDir, 1)
	}
	return path
}

func addFile(tw *tar.Writer, path, archivePath string) error {
	realPath := expandPath(path)
	file, err := os.Open(realPath)
	if err != nil {
		logger.Error("Failed to open file", zap.String("file_path", path), zap.Error(err))
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		logger.Error("Failed to stat file", zap.String("file_path", path), zap.Error(err))
		return err
	}

	header, err := tar.FileInfoHeader(stat, "")
	if err != nil {
		logger.Error("Failed to create tar header", zap.String("file_path", path), zap.Error(err))
		return err
	}
	header.Name = archivePath

	if err := tw.WriteHeader(header); err != nil {
		logger.Error("Failed to write tar header", zap.String("file_path", path), zap.Error(err))
		return err
	}

	if _, err := io.Copy(tw, file); err != nil {
		logger.Error("Failed to write file content to archive", zap.String("file_path", path), zap.Error(err))
		return err
	}
	return nil
}

func addDir(tw *tar.Writer, srcDir, archiveRoot string) {
	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		archivePath := filepath.Join(archiveRoot, relPath)
		addFile(tw, path, archivePath)
		return nil
	})
}
