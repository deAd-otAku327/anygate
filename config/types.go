package config

import (
	"time"

	"github.com/xakepp35/anygate/plugin"
)

type Root struct {
	Routes  map[string]string `yaml:"routes"`
	Server  Server            `yaml:"server"`
	Proxy   Proxy             `yaml:"proxy"`
	Static  Static            `yaml:"static"`
	Plugins []plugin.Spec     `yaml:"plugins"`
	Groups  []Root            `yaml:"groups"`
	Swagger []ServiceSpec     `yaml:"swagger"`
}

type ServiceSpec struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// 📦 Static — контракт между диском и путём.
type Static struct {
	Root               string        `yaml:"root"`                 // Корень сервируемых файлов,не оверрайдим
	Compress           bool          `yaml:"compress"`             // Включить сжатие
	CompressBrotli     bool          `yaml:"compress_brotli"`      // Использовать Brotli (если Compress = true)
	CompressZstd       bool          `yaml:"compress_zstd"`        // Использовать Zstd (если Compress = true)
	GenerateIndexPages bool          `yaml:"generate_index_pages"` // Генерировать индексные страницы
	IndexNames         []string      `yaml:"index_names"`          // Список index-файлов (index.html и т.п.)
	CacheDuration      time.Duration `yaml:"cache_duration"`       // Время жизни файлового кеша
	AcceptByteRange    bool          `yaml:"accept_byte_range"`    // Поддержка byte-range
	SkipCache          bool          `yaml:"skip_cache"`           // Не кешировать file handler'ы
}

type Server struct {
	ListenAddr                 string        `yaml:"listen_addr"`                // Адрес для listen
	ListenNetwork              string        `yaml:"listen_network"`             // Сеть для listen
	Name                       string        `yaml:"name"`                       // Имя сервера для заголовка Server
	Concurrency                int           `yaml:"concurrency"`                // Максимальное число одновременных соединений
	ReadBufferSize             int           `yaml:"read_buffer_size"`           // Размер буфера для чтения (влияет на макс. размер заголовков)
	WriteBufferSize            int           `yaml:"write_buffer_size"`          // Размер буфера для записи
	ReadTimeout                time.Duration `yaml:"read_timeout"`               // Таймаут на чтение запроса
	WriteTimeout               time.Duration `yaml:"write_timeout"`              // Таймаут на запись ответа
	IdleTimeout                time.Duration `yaml:"idle_timeout"`               // Таймаут простоя keep-alive соединения
	MaxConnsPerIP              int           `yaml:"max_conns_per_ip"`           // Максимум соединений с одного IP
	MaxRequestsPerConn         int           `yaml:"max_requests_per_conn"`      // Максимум запросов на одно соединение
	MaxIdleWorkerDuration      time.Duration `yaml:"max_idle_worker_duration"`   // Таймаут простаивающего worker'а
	TCPKeepalivePeriod         time.Duration `yaml:"tcp_keepalive_period"`       // Период TCP keepalive
	MaxRequestBodySize         int           `yaml:"max_request_body_size"`      // Максимальный размер тела запроса
	SleepOnConnLimitExceeded   time.Duration `yaml:"sleep_when_limit_exceeded"`  // Спать при превышении Concurrency
	DisableKeepalive           bool          `yaml:"disable_keepalive"`          // Отключить keep-alive соединения
	TCPKeepalive               bool          `yaml:"tcp_keepalive"`              // Включить TCP keep-alive
	ReduceMemoryUsage          bool          `yaml:"reduce_memory_usage"`        // Уменьшить потребление памяти (в ущерб CPU)
	GetOnly                    bool          `yaml:"get_only"`                   // Принимать только GET-запросы
	DisableMultipartForm       bool          `yaml:"disable_multipart_form"`     // Не парсить multipart формы
	LogAllErrors               bool          `yaml:"log_all_errors"`             // Логировать все ошибки
	SecureErrorLogMessage      bool          `yaml:"secure_error_log"`           // Не логировать чувствительное содержимое
	DisableHeaderNormalization bool          `yaml:"disable_header_normalizing"` // Не нормализовать имена заголовков
	NoDefaultServerHeader      bool          `yaml:"no_default_server_header"`   // Не добавлять заголовок Server
	NoDefaultDate              bool          `yaml:"no_default_date"`            // Не добавлять заголовок Date
	NoDefaultContentType       bool          `yaml:"no_default_content_type"`    // Не добавлять Content-Type по умолчанию
	KeepHijackedConns          bool          `yaml:"keep_hijacked_conns"`        // Не закрывать захваченные соединения
	CloseOnShutdown            bool          `yaml:"close_on_shutdown"`          // Добавлять Connection: close при выключении
	StreamRequestBody          bool          `yaml:"stream_request_body"`        // Включить потоковое чтение тела запроса
}

type Proxy struct {
	Timeout                       time.Duration `yaml:"timeout"`                      // infinite by default
	InsecureSkipVerify            bool          `yaml:"insecure_skip_verify"`         // set true to ignore invalid https certs
	RouteLenHint                  int           `yaml:"route_len_hint"`               // 64 by default, set more if you use wider get queries
	StatusBadGateway              int           `yaml:"status_bad_gateway"`           // 502 by default
	StatusGatewayTimeout          int           `yaml:"status_gateway_timeout"`       // 504 by default
	Name                          string        `yaml:"name"`                         // Имя клиента для заголовка User-Agent
	MaxConnsPerHost               int           `yaml:"max_conns_per_host"`           // Максимум соединений на каждый хост
	MaxIdleConnDuration           time.Duration `yaml:"max_idle_conn_duration"`       // Максимальное время простоя keep-alive соединения
	MaxConnDuration               time.Duration `yaml:"max_conn_duration"`            // Максимальное время жизни соединения
	MaxIdemponentCallAttempts     int           `yaml:"max_idemponent_call_attempts"` // Максимум повторов для идемпотентных запросов
	ReadBufferSize                int           `yaml:"read_buffer_size"`             // Размер буфера чтения (заголовки + тело)
	WriteBufferSize               int           `yaml:"write_buffer_size"`            // Размер буфера записи
	ReadTimeout                   time.Duration `yaml:"read_timeout"`                 // Максимальное время чтения ответа (включая тело)
	WriteTimeout                  time.Duration `yaml:"write_timeout"`                // Максимальное время записи запроса (включая тело)
	MaxResponseBodySize           int           `yaml:"max_response_body_size"`       // Максимальный размер тела ответа
	MaxConnWaitTimeout            time.Duration `yaml:"max_conn_wait_timeout"`        // Таймаут ожидания свободного соединения
	ConnPoolStrategyLifo          bool          `yaml:"conn_pool_strategy_lifo"`      // Стратегия пула соединений: fifo (по умолчанию) или lifo
	NoDefaultUserAgentHeader      bool          `yaml:"no_default_user_agent"`        // Не добавлять заголовок User-Agent по умолчанию
	DialDualStack                 bool          `yaml:"dial_dual_stack"`              // Использовать одновременно IPv4 и IPv6
	DisableHeaderNamesNormalizing bool          `yaml:"disable_header_normalizing"`   // Не нормализовать имена заголовков (важно для прокси)
	DisablePathNormalizing        bool          `yaml:"disable_path_normalizing"`     // Не нормализовать пути запроса
	StreamResponseBody            bool          `yaml:"stream_response_body"`         // Включить потоковое чтение тела ответа
}
