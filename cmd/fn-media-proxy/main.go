// fn-media-proxy：把飞牛影视伪装成 Emby 服务端
//
// 客户端连本服务 → 本服务翻译成飞牛私有接口 → 播放时 302 到网盘直链。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/user/fn-media-proxy/internal/emby"
	"github.com/user/fn-media-proxy/internal/fn"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	var (
		listen  = flag.String("listen", env("LISTEN", ":8096"), "监听地址")
		fnBase  = flag.String("fn", env("FN_BASE", ""), "飞牛影视地址，如 http://192.168.100.10:8005")
		name    = flag.String("name", env("SERVER_NAME", "fnOS Media"), "在 Emby 客户端里显示的服务器名")
		verbose = flag.Bool("v", env("VERBOSE", "") != "", "打印访问日志（默认开启，-v=false 关闭）")
	)
	flag.Parse()

	if *fnBase == "" {
		log.Fatal("必须指定飞牛地址：-fn http://192.168.100.10:8005 或环境变量 FN_BASE")
	}

	client := fn.NewClient(*fnBase)

	h := emby.NewHandler(client, *name)
	h.Version = "4.8.0.0"
	if !*verbose {
		h.OnLog = func(string, ...any) {}
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 15 * time.Second,
		// 不设全局写超时：流式响应可能很长
	}

	go func() {
		log.Printf("fn-media-proxy 启动：%s -> %s（Emby 兼容端口 %s）", *listen, *fnBase, *listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务启动失败: %v", err)
		}
	}()

	// 启动自检：探测飞牛连通性（不带账号，只确认签名机制与端点存在）
	go selfCheck(client)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("正在关闭...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// selfCheck 启动时验证飞牛端点可达、签名机制仍然有效。
func selfCheck(c *fn.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.MediaDBList(ctx)
	if err == nil {
		log.Println("自检：飞牛接口连通（未登录状态返回成功，说明无鉴权）")
		return
	}
	if apiErr, ok := err.(*fn.APIError); ok {
		switch apiErr.Code {
		case 5000:
			log.Printf("自检警告：签名校验失败（code=5000）——飞牛版本可能已变更 authx 密钥，请更新 internal/fn/const.go")
		default:
			log.Printf("自检：飞牛返回 code=%d（通常为未登录，属正常）", apiErr.Code)
		}
		return
	}
	log.Printf("自检警告：无法连接飞牛 %s: %v", c.BaseURL, err)
}
