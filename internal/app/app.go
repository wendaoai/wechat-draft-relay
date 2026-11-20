package app

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gogf/gf/v2/net/ghttp"

	controller "github.com/wendaoai/wechat-draft-relay/internal/controller/wechatdraft"
	logic "github.com/wendaoai/wechat-draft-relay/internal/logic/wechatdraft"
)

func Main() {
	options := flag.NewFlagSet("wechat-draft", flag.ContinueOnError)
	options.SetOutput(os.Stderr)
	configPath := options.String("config", "", "配置文件路径；默认读取可执行程序同目录的.env")
	initConfig := options.Bool("init-config", false, "生成同目录配置和随机服务密钥，不覆盖已有文件")
	if err := options.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(1)
	}
	if options.NArg() != 0 {
		log.Print("wechat-draft: unexpected positional arguments")
		os.Exit(1)
	}
	if *initConfig {
		if err := logic.InitConfig(*configPath); err != nil {
			log.Print("wechat-draft: configuration initialization failed; existing configuration was not overwritten")
			os.Exit(1)
		}
		fmt.Println("已生成.env；密钥仅保存在配置文件中。现在可直接启动服务。")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *configPath); err != nil {
		log.Print("wechat-draft: startup or shutdown failed; check configuration, directory ownership and instance lock")
		os.Exit(1)
	}
}

func run(ctx context.Context, configPath string) error {
	c, err := logic.LoadConfigFile(configPath)
	if err != nil {
		return err
	}
	manager, err := logic.NewManager(ctx, c, logic.NewWechat(c.Workers))
	if err != nil {
		return err
	}
	restoreLogging, err := StartLogging(c.DataDir)
	if err != nil {
		deadline, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = manager.Close(deadline)
		return err
	}
	defer restoreLogging()
	s := ghttp.GetServer("wechat-draft")
	if err = controller.Configure(s, c); err == nil {
		controller.Bind(s, manager, c.ServiceKey)
		err = s.Start()

	}
	if err != nil {
		deadline, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = manager.Close(deadline)
		return err
	}
	log.Print("wechat-draft: independent service ready")
	<-ctx.Done()
	deadline, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	shutdown := make(chan error, 1)
	go func() { shutdown <- s.Shutdown() }()
	err = manager.Close(deadline)
	select {
	case serverErr := <-shutdown:
		if err != nil {
			return err
		}
		return serverErr
	case <-deadline.Done():
		return deadline.Err()
	}
}
