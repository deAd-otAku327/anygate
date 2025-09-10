package handler

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fasthttp"
	"github.com/xakepp35/anygate/config"
	"github.com/xakepp35/anygate/utils"
)

type connParams struct {
	targetURL *url.URL
	proxyFrom string
	proxyTo   string

	timeout              time.Duration
	statusBadGateway     int
	statusGatewayTimeout int
	readBufferSize       int
	writeBufferSize      int
}

// 🚀 NewProxy — проксирует с сохранением raw query и без мутации исходного ctx.Request
func NewProxy(from, to string, cfg config.Proxy) fasthttp.RequestHandler {
	if cfg.StatusBadGateway == 0 {
		cfg.StatusBadGateway = fasthttp.StatusBadGateway
	}
	if cfg.StatusGatewayTimeout == 0 {
		cfg.StatusGatewayTimeout = fasthttp.StatusGatewayTimeout
	}
	if cfg.RouteLenHint == 0 {
		cfg.RouteLenHint = 64
	}

	pathBuilder := utils.NewPathBuilder(to, len(from), cfg.RouteLenHint)

	client := NewClient(cfg)

	dialer := &websocket.Dialer{
		ReadBufferSize:   cfg.ReadBufferSize,
		WriteBufferSize:  cfg.WriteBufferSize,
		HandshakeTimeout: cfg.Timeout,
	}

	connParams := &connParams{
		proxyFrom: from,
		proxyTo:   to,

		timeout:              cfg.Timeout,
		statusBadGateway:     cfg.StatusBadGateway,
		statusGatewayTimeout: cfg.StatusGatewayTimeout,
		readBufferSize:       cfg.ReadBufferSize,
		writeBufferSize:      cfg.ReadBufferSize,
	}

	return func(ctx *fasthttp.RequestCtx) {
		// Собираем базовую цель (scheme+host+path) через builder
		target := pathBuilder.Build(ctx.Path())

		// Приклеиваем СЫРОЙ query из входящего запроса (без переэнкодинга)
		if raw := ctx.URI().QueryString(); len(raw) > 0 {
			if strings.Contains(target, "?") {
				target += "&" + string(raw)
			} else {
				target += "?" + string(raw)
			}
		}

		targetURL, err := url.Parse(target)
		if err != nil {
			ctx.Error("Internal server error", fasthttp.StatusInternalServerError)
			log.Error().
				Err(err).
				Str("rawURL", target).
				Msg("URL parsing failed")
			return
		}

		connParams.targetURL = targetURL

		// Определяем запрос с апгрейдом на вебсокет
		if string(ctx.Method()) == "GET" && websocket.FastHTTPIsWebSocketUpgrade(ctx) {

			handleWebSocketConnection(ctx, dialer, connParams)
			return
		}

		handleHTTPConnection(ctx, client, connParams)
	}
}

func handleHTTPConnection(ctx *fasthttp.RequestCtx, client *fasthttp.Client, cp *connParams) {
	// Готовим отдельный запрос (не трогаем ctx.Request)
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer func() {
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(resp)
	}()

	ctx.Request.CopyTo(req)                  // копируем метод/заголовки/тело
	req.SetRequestURI(cp.targetURL.String()) // абсолютный URI с нашим query
	req.SetTimeout(cp.timeout)               // если задан

	// Проставим корректный Host для апстрима (на всякий случай)
	if h := req.URI().Host(); len(h) > 0 {
		req.Header.SetHostBytes(h)
	}

	log.Info().
		Str("from", cp.proxyFrom).
		Str("to", cp.proxyTo).
		Str("method", string(req.Header.Method())).
		Str("target", cp.targetURL.String()).
		Bytes("raw_query_in", ctx.URI().QueryString()).
		Msg("proxy -> upstream")

	// Шлём запрос и кладём ответ прямо в ctx.Response
	if err := client.Do(req, resp); err != nil {
		if err == fasthttp.ErrTimeout {
			ctx.Error(`{"error":"timeout"}`, cp.statusGatewayTimeout)
			log.Error().Err(err).Str("from", cp.proxyFrom).Str("to", cp.proxyTo).Msg("timeout")
			return
		}
		ctx.Error(`{"error":"`+err.Error()+`"}`, cp.statusBadGateway)
		log.Error().Err(err).Str("from", cp.proxyFrom).Str("to", cp.proxyTo).Msg("gateway")
		return
	}

	// Копируем ответ
	ctx.Response.SetStatusCode(resp.StatusCode())
	resp.Header.CopyTo(&ctx.Response.Header)
	ctx.Response.SetBodyRaw(resp.Body()) // zero-copy set

	log.Info().
		Str("to", cp.proxyTo).
		Int("status", resp.StatusCode()).
		Msg("Proxy success")
}

func handleWebSocketConnection(ctx *fasthttp.RequestCtx, dialer *websocket.Dialer, cp *connParams) {
	// Схема подключения - вебсокет (меняем если указано http(s))
	if strings.HasPrefix(cp.targetURL.Scheme, "http") {
		cp.targetURL.Scheme = strings.Replace(cp.targetURL.Scheme, "http", "ws", 1)
	}

	log.Info().
		Str("from", cp.proxyFrom).
		Str("to", cp.proxyTo).
		Str("target", cp.targetURL.String()).
		Bytes("raw_query_in", ctx.URI().QueryString()).
		Msg("proxy -> websocket upstream")

	// Устанавливаем соединение с сервером
	serverConn, resp, err := dialer.Dial(cp.targetURL.String(), nil)
	if err != nil {
		ctx.Error(`{"error":"`+err.Error()+`"}`, fasthttp.StatusBadGateway)
		log.Error().Err(err).Str("from", cp.proxyFrom).Str("to", cp.proxyTo).Msg("Failed to connect to websocket server")
		return
	}
	defer resp.Body.Close()

	// Дополнительная проверка на успешность хендшейка
	if resp.StatusCode != fasthttp.StatusSwitchingProtocols {
		ctx.Error(`{"error":"bad handshake"}`, fasthttp.StatusBadGateway)
		log.Error().Err(err).Str("from", cp.proxyFrom).Str("to", cp.proxyTo).Msg("bad handshake")
		return
	}

	connUpgrader := websocket.FastHTTPUpgrader{
		ReadBufferSize:   cp.readBufferSize,
		WriteBufferSize:  cp.writeBufferSize,
		HandshakeTimeout: cp.timeout,
		CheckOrigin: func(ctx *fasthttp.RequestCtx) bool {
			return true
		},
	}

	// Регистрируем хендлер пайпа между клиентом и сервером (обработка клиентских подключений)
	err = connUpgrader.Upgrade(ctx, func(clientConn *websocket.Conn) {
		defer func() {
			// Для надежности
			serverConn.Close()
			clientConn.Close()

			log.Info().
				Str("from", clientConn.RemoteAddr().String()).
				Str("to", serverConn.RemoteAddr().String()).
				Msg("websocket connection pipe closed")
		}()

		var wg sync.WaitGroup
		wg.Add(2)

		// Две горутины проксируют трафик между сервером и клиентом (в обе стороны)
		go func() {
			defer wg.Done()
			forwardWebSocket(ctx, clientConn, serverConn)
		}()

		go func() {
			defer wg.Done()
			forwardWebSocket(ctx, serverConn, clientConn)
		}()

		log.Info().
			Str("from", clientConn.RemoteAddr().String()).
			Str("to", serverConn.RemoteAddr().String()).
			Msg("websocket connection pipe opened")

		// Ожидание момента когда прервется проксирование на оба направления
		wg.Wait()
	})

	if err != nil {
		ctx.Error(`{"error":"connection upgrade error"}`, fasthttp.StatusUpgradeRequired)
		log.Error().Err(err).Str("from", cp.proxyFrom).Str("to", cp.proxyTo).Msg("WebSocket upgrade error")
	}

	// ВАЖНО: блокирование завершения данной функции препятствует работе обработчика клиентских подключений (см выше: Upgrade() )
}

// Проксирование вебсокет трафика src -> dst
func forwardWebSocket(ctx *fasthttp.RequestCtx, src, dst *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			initiatedClosing := false

			// Ждем сообщения
			messageType, message, err := src.ReadMessage()
			if err != nil {
				// Логируем, если закрытие не штатное или другая ошибка.
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Error().Err(err).Str("src", src.RemoteAddr().String()).Msg("Websocket read error")
				}

				// Если ошибка не связана с закрытием src-соединения - просто продолжаем ждать следующего сообщения
				if _, ok := err.(*websocket.CloseError); !ok {
					continue
				}

				// Если src закрыт - необходимо инициировать разрыв пайпа и отправить close-message на dst, который инициирует его штатное закрытие
				messageType = websocket.CloseMessage
				message = websocket.FormatCloseMessage(websocket.CloseGoingAway, "pipe closing initiated")
				initiatedClosing = true
			}

			// Пишем сообщение на dst
			// В случае получения перед этим close-message, оно будет переотправлено на dst (уже закрытое), во имя лучшей читаемости
			err = dst.WriteMessage(messageType, message)
			if err != nil {
				fmt.Println(err)
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) && !errors.Is(err, websocket.ErrCloseSent) {
					log.Error().Err(err).Str("dst", dst.RemoteAddr().String()).Msg("Websocket write error")
				}
			}

			// Если было инициировано закрытие - выходим
			if initiatedClosing {
				return
			}
		}
	}
}
