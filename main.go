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
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
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
	LogFile        bool           `yaml:"enable_log_file"`
}

var cfg Config

type Bot struct {
	BotToken           string `yaml:"token"`
	SendArchiveOnStart bool   `yaml:"send_archive_on_start"`
}

type Archive struct {
	ExcludeObjects []string `yaml:"exclude_objects"`
	ArchiveName    string   `yaml:"archive_name"`
	TempBackupDir  string   `yaml:"temp_backup_dir"`
	Timezone       string   `yaml:"timezone"`
	BackupTime     []string `yaml:"backup_times"`
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

type memorySession struct {
	mux  sync.RWMutex
	data []byte
}

func (s *memorySession) LoadSession(context.Context) ([]byte, error) {
	if s == nil {
		return nil, session.ErrNotFound
	}

	s.mux.RLock()
	defer s.mux.RUnlock()

	if len(s.data) == 0 {
		return nil, session.ErrNotFound
	}

	cpy := append([]byte(nil), s.data...)

	return cpy, nil
}

func (s *memorySession) StoreSession(ctx context.Context, data []byte) error {
	s.mux.Lock()
	s.data = data
	s.mux.Unlock()
	return nil
}

func setuplog() *zap.Logger {
	encoderCfg := zapcore.EncoderConfig{
		TimeKey:     "time",
		LevelKey:    "level",
		MessageKey:  "msg",
		EncodeTime:  zapcore.TimeEncoderOfLayout("2006.01.02 15:04:05.000"),
		EncodeLevel: zapcore.CapitalLevelEncoder,
	}

	encoder := zapcore.NewConsoleEncoder(encoderCfg)
	consoleCore := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), zapcore.InfoLevel)

	var cores []zapcore.Core
	cores = append(cores, consoleCore)

	if cfg.LogFile {
		logFile, err := os.OpenFile("backupbot.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			panic(fmt.Sprintf("cannot open log file: %v", err))
		}
		fileCore := zapcore.NewCore(encoder, zapcore.AddSync(logFile), zapcore.InfoLevel)
		cores = append(cores, fileCore)
	}

	log := zap.New(zapcore.NewTee(cores...))
	return log
}

var log = setuplog()

func main() {
	data, err := os.ReadFile("backup_config.yml")
	if err != nil {
		panic(fmt.Sprintf("Failed to load config file: %v", err))
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		panic(fmt.Sprintf("Failed to parse config: %v", err))
	}

	log = setuplog()
	log.Info("\n\nBot started\n")

	ctx := context.Background()
	bot, err := telego.NewBot(cfg.Bot.BotToken)
	if err != nil {
		log.Fatal("Failed to load bot", zap.Error(err))
	}

	if cfg.Bot.SendArchiveOnStart {
		sendBackup(bot, ctx, cfg.BackupChatData.ID, cfg.BackupChatData.ThreadID)
		log.Info("Startup backup succesfully sended")
	}

	updates, _ := bot.UpdatesViaLongPolling(ctx, nil)
	log.Info("Polling started")
	_, _ = bot.SendMessage(ctx, tu.Message(tu.ID(5259467241), "Polling started"))
	bh, _ := th.NewBotHandler(bot, updates)

	location, err := time.LoadLocation(cfg.Archive.Timezone)
	if err != nil {
		log.Fatal("Failed to load timezone", zap.Error(err))
	}
	c := cron.New(cron.WithLocation(location))

	for _, t := range cfg.Archive.BackupTime {
		spec := t
		_, err := c.AddFunc(spec, func() {
			sendBackup(bot, ctx, cfg.BackupChatData.ID, cfg.BackupChatData.ThreadID)
			log.Info("CronJob successfully done", zap.String("time", t))
		})
		if err != nil {
			log.Error("Failed to add CronJob", zap.Error(err))
		} else {
			log.Info("CronJob added", zap.String("time", t))
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
		log.Info("The /start command has been successfully processed",
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

func sendBackup(bot *telego.Bot, ctx context.Context, target_chat_id int64, target_therad_id int) {
	archive_file, err := createArchive()
	if err != nil {
		log.Error("Failed to open archive to send", zap.Error(err))
	}

	defer func() {
		archive_file.Close()
		os.Remove(archive_file.Name())
	}()

	archive_file_info, _ := archive_file.Stat()

	if archive_file_info.Size() < 20971510 {
		log.Info("Sending archive via bot", zap.String("file_name", archive_file.Name()), zap.Int64("file_size", archive_file_info.Size()))
		_, err = bot.SendDocument(ctx, tu.Document(
			tu.ID(target_chat_id),
			tu.File(archive_file),
		).WithMessageThreadID(target_therad_id))
		if err != nil {
			log.Error("Failed to send archive", zap.String("file_name", archive_file.Name()), zap.Error(err))
			_, _ = bot.SendMessage(ctx, tu.Message(
				tu.ID(target_chat_id),
				fmt.Sprintf("Failed to send archive <code>%v</code>: %v", archive_file.Name(), err),
			).WithParseMode("HTML").WithMessageThreadID(target_therad_id),
			)
		}
		log.Info("Archive succesfully sended via bot")

	} else {
		log.Info("Sending archive via client", zap.String("file_name", archive_file.Name()), zap.Int64("file_size", archive_file_info.Size()))

		sessionStorage := &memorySession{}
		client := telegram.NewClient(cfg.ClientApiData.ID, cfg.ClientApiData.Hash, telegram.Options{
			SessionStorage: sessionStorage,
			Logger:         log,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		err := client.Run(ctx, func(ctx context.Context) error {

			status, err := client.Auth().Status(ctx)
			if err != nil {
				return err
			}
			if !status.Authorized {
				if _, err := client.Auth().Bot(ctx, cfg.Bot.BotToken); err != nil {
					return err
				}
			}

			log.Info(fmt.Sprintf("%v", *status))

			api := tg.NewClient(client)
			u := uploader.NewUploader(api)
			sender := message.NewSender(api).WithUploader(u)

			log.Info("Uploading archive...")

			upload, err := u.FromPath(ctx, archive_file.Name())
			if err != nil {
				log.Error("Failed to upload archive via client", zap.String("file_name", archive_file.Name()), zap.Error(err))
				return err
			}
			log.Info("Archive succesfully uploaded. Sending archive...")

			document := message.UploadedDocument(upload, html.String(nil, fmt.Sprintf("test_html_string <code>%v</code>", strings.Replace(archive_file.Name(), "_", " ", 0))))

			target := sender.To(&tg.InputPeerChat{ChatID: target_chat_id})

			_, err = target.Media(ctx, document)
			if err != nil {
				log.Error("Failed to send archive via client", zap.String("file_name", archive_file.Name()), zap.Error(err))
				return err
			}
			log.Info("Archive succesfully sended via client")

			return nil
		})

		if err != nil {
			log.Fatal("Telegram client failed", zap.Error(err))
		}
	}
}

func createArchive() (*os.File, error) {
	loc, _ := time.LoadLocation(cfg.Archive.Timezone)

	timestamp := time.Now().In(loc).Format("2006-01-02_15-04-05")
	hostname, err := os.Hostname()
	if err != nil {
		log.Warn("Failed to get hostname", zap.Error(err))
	}

	outFile, err := os.Create(fmt.Sprintf("backup_%s_%s.tar.gz", hostname, timestamp))

	if err != nil {
		log.Error("Failed to create archive file", zap.Error(err))
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
			return path
		}
		return strings.Replace(path, "~", homeDir, 1)
	}
	return path
}

func isExcluded(path string) bool {
	base := filepath.Base(path)

	for _, pattern := range cfg.Archive.ExcludeObjects {
		if base == pattern {
			return true
		}

		matched, err := filepath.Match(pattern, base)
		if err == nil && matched {
			return true
		}

		matched, err = filepath.Match(pattern, path)
		if err == nil && matched {
			return true
		}
	}
	return false
}

func addFile(tw *tar.Writer, path, archivePath string) error {
	realPath := expandPath(path)

	if isExcluded(realPath) {
		log.Info("File excluded from archive", zap.String("file_path", realPath))
		return nil
	}

	file, err := os.Open(realPath)
	if err != nil {
		log.Error("Failed to open file", zap.String("file_path", path), zap.Error(err))
		return err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		log.Error("Failed to stat file", zap.String("file_path", path), zap.Error(err))
		return err
	}

	header, err := tar.FileInfoHeader(stat, "")
	if err != nil {
		log.Error("Failed to create tar header", zap.String("file_path", path), zap.Error(err))
		return err
	}
	header.Name = archivePath

	if err := tw.WriteHeader(header); err != nil {
		log.Error("Failed to write tar header", zap.String("file_path", path), zap.Error(err))
		return err
	}

	if _, err := io.Copy(tw, file); err != nil {
		log.Error("Failed to write file content to archive", zap.String("file_path", path), zap.Error(err))
		return err
	}
	return nil
}

func addDir(tw *tar.Writer, cfg_srcDir, archiveRoot string) {
	srcDir := expandPath(cfg_srcDir)

	if isExcluded(srcDir) {
		log.Info("Directory excluded from archive", zap.String("dir_path", srcDir))
		return
	}

	filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if isExcluded(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
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
