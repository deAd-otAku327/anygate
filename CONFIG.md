# 🧾 Конфиг anygate.yml, по братски explained 

[Вернуться обратно](README.md)

## 📌 Пример конфига anygate.yml

```yaml
# Основная карта маршрутов
routes:
  /api/users/: http://users-backend:8081/api/ # Простая маршрутизация URL -> backend
  GET POST /api/entity: https://e.com/api/ent # Прокси только выбранных методов
  /: /dist/                                   # Статическая маршрутизация для твоего SPA

# Настройки прокси сервера
proxy:
  timeout: 5s                                 # Таймаут при проксировании запроса (0 = бесконечный)
  insecure_skip_verify: false                 # Не проверять валидность TLS-сертификатов (для самоподписанных)
  route_len_hint: 64                          # Подсказка длины строки маршрута (для оптимизации буфера, например для длинных GET кверей > 64 символа)
  status_bad_gateway: 502                     # Код ответа при ошибке шлюза
  status_gateway_timeout: 504                 # Код ответа при таймауте до бэкенда
  name: "anygate"                             # Имя клиента (используется в User-Agent)
  max_conns_per_host: 512                     # Максимум соединений к каждому хосту
  max_idle_conn_duration: 10s                 # Время жизни idle-соединения (keep-alive)
  max_conn_duration: 2m                       # Максимальная продолжительность жизни соединения
  max_idemponent_call_attempts: 3             # Кол-во повторных попыток для идемпотентных методов (GET, HEAD и т.д.)
  read_buffer_size: 4096                      # Размер буфера чтения (в байтах)
  write_buffer_size: 4096                     # Размер буфера записи (в байтах)
  read_timeout: 30s                           # Таймаут на чтение ответа от бэкенда
  write_timeout: 15s                          # Таймаут на отправку запроса бэкенду
  max_response_body_size: 10485760            # Максимальный размер тела ответа (в байтах), 10 MB
  max_conn_wait_timeout: 1s                   # Максимальное время ожидания свободного соединения
  conn_pool_strategy_lifo: false              # true = LIFO стратегия пула, false = FIFO (по умолчанию)
  no_default_user_agent: false                # true = не добавлять заголовок User-Agent по умолчанию
  dial_dual_stack: false                      # true = Использовать как IPv4, так и IPv6
  disable_header_normalizing: false           # true = не изменять регистр заголовков (важно для прокси)
  disable_path_normalizing: false             # true = не нормализовать путь (оставить /a//b как есть)
  stream_response_body: false                 # true = читать тело ответа потоково

# Настройки статик сервера
static:
  root: "./public"                            # Путь к статическим файлам, обычно не нужен, задаётся через значение в routes
  compress: true                              # Включить сжатие, может помочь при слабом bandwidth
  compress_brotli: true                       # Использовать Brotli
  compress_zstd: false                        # Использовать Zstd
  generate_index_pages: true                  # Генерировать index.html если не найден - медленно, при большом колве файлов >1k
  index_names: ["index.html"]                 # Какие файлы считать индексными
  cache_duration: 10m                         # Время жизни кеша
  allow_empty_root: false                     # Разрешить пустой root
  accept_byte_range: true                     # Поддержка byte-range запросов
  skip_cache: true                            # Отключить кеш file handler'ов, например для дева

# Настройки HTTP сервера
server:
  listen_addr: ":80"                          # Адрес, на котором слушает AnyGate (":80" означает все интерфейсы на 80 порту)
  listen_network: tcp4                        # Тип сети: "tcp", "unix" и т.п.
  name: "AnyGate"                             # Заголовок Server в HTTP-ответах
  concurrency: 262144                         # Максимальное число одновременных соединений
  read_buffer_size: 4096                      # Размер буфера чтения (в байтах)
  write_buffer_size: 4096                     # Размер буфера записи
  read_timeout: 5s                            # Таймаут на чтение запроса
  write_timeout: 5s                           # Таймаут на отправку ответа
  idle_timeout: 30s                           # Keep-alive таймаут
  max_conns_per_ip: 50                        # Ограничение по IP
  max_requests_per_conn: 100                  # Сколько запросов можно выполнить за одно keep-alive соединение
  max_idle_worker_duration: 10s               # Сколько worker может простоять без дела
  tcp_keepalive_period: 15s                   # Период TCP keepalive
  max_request_body_size: 1048576              # Максимальный размер тела запроса (1 MB)
  sleep_when_limit_exceeded: 1s               # Если лимит достигнут — поспать перед принятием новых соединений
  disable_keepalive: false                    # Отключить keep-alive
  tcp_keepalive: true                         # Включить TCP keep-alive
  reduce_memory_usage: false                  # Уменьшить память в ущерб CPU
  get_only: false                             # Только GET-запросы
  disable_multipart_form: true                # Не обрабатывать multipart формы
  log_all_errors: true                        # Логировать любые ошибки
  secure_error_log: true                      # Не логировать чувствительные данные
  disable_header_normalizing: false           # Отключить нормализацию заголовков
  no_default_server_header: false             # Не вставлять Server: AnyGate
  no_default_date: false                      # Не вставлять Date
  no_default_content_type: false              # Не вставлять Content-Type
  keep_hijacked_conns: false                  # Не закрывать hijacked соединения
  close_on_shutdown: true                     # Вставить Connection: close при завершении
  stream_request_body: true                   # Читать тело запроса потоково

# Настройки SwaggerUI c поддержкой мультиинтерфейса для набора сервисов
swagger:
  - name: service-1 # Тестовые сервисы
    url: https://petstore.swagger.io/v2/swagger

  - name: service-2
    url: https://httpbin.org/spec.json

  - name: service-3
    url: https://catfact.ninja/docs

# Плагины - миддлвары, которые вешаюся на хендлер
plugins:
  - kind: "logger"                            # Имя плагина (например, встроенный логгер)
    args:                                     # Конфиг специфичен для плагина
      level: "info"                           
  - kind: "rate-limiter"
    args:
      rpm: 100
      burst: 200

# Группы
groups:
  - routes:
      "/v1/": "http://service-v1:9000"        # Группировка подмаршрутов (префиксный неймспейс)
    proxy:
      timeout: 5s
    plugins:
      - name: "jwt-auth"
        config:
          header: "X-API-Key"
```
