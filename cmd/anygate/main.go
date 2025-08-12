package main

import (
	"net"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/xakepp35/anygate/config"
	"github.com/xakepp35/anygate/handler"
	"github.com/xakepp35/anygate/router"
	"github.com/xakepp35/pkg/xlog"
)

func init() {
	xlog.Init()
}

func main() {
	log.Info().Msg("hello world!!!!")
	// init
	rootConfig := config.NewRoot()
	log.Debug().Any("root", rootConfig).Msg("config.NewRoot")

	// mainRouter := router.New()
	// handler.Register(mainRouter, rootConfig)
	// httpServer := handler.NewServer(rootConfig.Server, mainRouter.Handler)

	mainRouter := router.New()
	handler.Register(mainRouter, rootConfig)
	httpServer := handler.NewServer(rootConfig.Server, mainRouter.Handler)

	// listen
	ln, err := net.Listen(rootConfig.Server.ListenNetwork, rootConfig.Server.ListenAddr)
	if err != nil {
		log.Fatal().Str("net", rootConfig.Server.ListenNetwork).Str("addr", rootConfig.Server.ListenAddr).Msg("net.Listen")
	}
	// lifecycle
	log.Info().Str("net", rootConfig.Server.ListenNetwork).Str("addr", rootConfig.Server.ListenAddr).Msg("startup")
	startedAt := time.Now().UTC()
	err = httpServer.Serve(ln)
	if err != nil {
		log.Fatal().Err(err).Msg("httpServer.Serve")
	}
	// shutdown
	log.Info().Time("started_at", startedAt).Dur("runtime", time.Since(startedAt)).Msg("shutdown")
}
