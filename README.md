# Agent Service

Описание: сервис для управления контейнерами-агентами с поддержкой реального Docker API.

## Возможности

- **Реализация интерфейса AgentOrchestratorInterface**
- **Интеграция с образом ghcr.io/vasenin26/agentmodule**
- **Автоматическая генерация SSH ключей для каждого агента**
- **Безопасное хранение SSH ключей в файловой системе**
- **Управление Docker контейнерами**
- **Аутентификация в Docker Registry**
- **Prometheus метрики**
- **Логирование операций**

## SSH Ключи

Для каждого агента автоматически генерируется пара SSH ключей:
- **Публичный ключ** возвращается в ответе API
- **Приватный ключ** передается в контейнер через переменную окружения `SSH_PRIVATE_KEY`
- Используется RSA 2048-bit шифрование
- Ключи уникальны для каждого агента
- **Переиспользование**: при повторном запуске агента с тем же ID используются существующие ключи

### Хранение SSH ключей

SSH ключи сохраняются в файловой системе:
- **Приватные ключи**: `{SSH_KEYS_DIR}/{agentId}_private.pem` (права 600)
- **Публичные ключи**: `{SSH_KEYS_DIR}/{agentId}_public.pub` (права 644)
- **Постоянное хранение**: ключи сохраняются после остановки агента
- **Переиспользование**: агент может быть перезапущен с тем же ключом
- **Безопасность**: папка создается с правами 700

## Переменные окружения в контейнере

Каждый запущенный агент получает следующие переменные окружения:

| Переменная | Описание | Источник |
|------------|----------|----------|
| `AGENT_ID` | Уникальный ID агента | Из запроса |
| `API_TOKEN` | API токен агента | Из запроса |
| `SERVER` | URL сервера | Автоматически |
| `SSH_PRIVATE_KEY` | Приватный SSH ключ | Генерируется автоматически |
| `API_HOST` | Хост API | Из конфигурации |
| `OPENAI_MODEL` | Модель OpenAI | Из конфигурации |
| `OPENAI_API_KEY` | API ключ OpenAI | Из конфигурации |
| `GIT_USER_NAME` | Имя пользователя Git | Из конфигурации |
| `GIT_USER_EMAIL` | Email пользователя Git | Из конфигурации |

## API примеры (JSON)

### AgentOrchestratorInterface API
- `POST /orchestrator/start-agent` — запустить агента с SSH ключами
  ```json
  {
    "agentId": "550e8400-e29b-41d4-a716-446655440000",
    "token": "agent-specific-token-123"
  }
  ```
- `POST /orchestrator/stop-agent/{agentId}` — остановить агента по ID
  ```
  POST /orchestrator/stop-agent/550e8400-e29b-41d4-a716-446655440000
  ```
- `POST /orchestrator/start-process` — запустить процесс
  ```json
  {
    "taskType": "data-processing"
  }
  ```

### SSH Key Management API
- `GET /ssh/keys/{agentId}` — получить SSH ключи агента (в разработке)

### Упрощенный API для остановки агента

Теперь для остановки агента требуется только `agentId` в URL пути:
- **Метод**: `POST`
- **URL**: `/orchestrator/stop-agent/{agentId}`
- **Параметры**: `agentId` в пути URL
- **Ответ**: `{"status": "agent stopped"}`

## Логика работы с SSH ключами

1. **Первый запуск агента**: генерируются новые SSH ключи и сохраняются в файловую систему
2. **Повторный запуск агента**: используются существующие ключи из файловой системы
3. **Остановка агента**: SSH ключи сохраняются для возможного перезапуска
4. **Безопасность**: каждый агент имеет уникальные ключи, привязанные к его ID

## API токен агента

В системе используется один API токен для каждого агента:

### API_TOKEN
- **Источник**: передается клиентом в запросе `POST /orchestrator/start-agent`
- **Назначение**: аутентификация агента с внешними API
- **Уникальность**: каждый агент может иметь свой токен
- **Пример**: `"token": "agent-api-token-123"`

### Использование токена
- Агент использует `API_TOKEN` для аутентификации с внешними сервисами
- Токен передается в переменной окружения `API_TOKEN` внутри контейнера
- Каждый агент получает свой уникальный токен при создании

## Требования

- Docker Engine (локально или удаленно)
- Go 1.20+
- Доступ к Docker API (по умолчанию через Docker socket)

## Конфигурация

### Переменные окружения

| Параметр | Переменная | По умолчанию | Описание |
|----------|------------|--------------|----------|
| HTTP Port | `HTTP_PORT` | `8080` | Порт HTTP сервера |
| Registry Server | `REGISTRY_SERVER` | - | Адрес Docker registry |
| Registry Username | `REGISTRY_USERNAME` | - | Имя пользователя |
| Registry Password | `REGISTRY_PASSWORD` | - | Пароль или токен |
| **API Host** | `API_HOST` | - | Хост API для агентов |
| **OpenAI Model** | `OPENAI_MODEL` | - | Модель OpenAI |
| **OpenAI API Key** | `OPENAI_API_KEY` | - | API ключ OpenAI |
| **Git User Name** | `GIT_USER_NAME` | - | Имя пользователя Git |
| **Git User Email** | `GIT_USER_EMAIL` | - | Email пользователя Git |
| **SSH Keys Directory** | `SSH_KEYS_DIR` | `./keys` | Папка для хранения SSH ключей |

### Пример конфигурации

```bash
# HTTP Configuration
HTTP_PORT=8080

# Docker Configuration
DOCKER_HOST=unix:///var/run/docker.sock
REGISTRY_SERVER=ghcr.io
REGISTRY_USERNAME=your-username
REGISTRY_PASSWORD=your-token

# Agent Configuration
API_HOST=https://api.example.com
OPENAI_MODEL=gpt-4
OPENAI_API_KEY=your-openai-api-key
GIT_USER_NAME=Your Name
GIT_USER_EMAIL=your.email@example.com

# SSH Keys Storage
SSH_KEYS_DIR=./keys
```

### GitHub Container Registry (GHCR)

Для работы с GitHub Container Registry:

1. **Создайте Personal Access Token**:
   - GitHub → Settings → Developer settings → Personal access tokens
   - Права: `read:packages`, `write:packages`, `delete:packages`

2. **Настройте переменные**:
   ```bash
   export REGISTRY_SERVER=ghcr.io
   export REGISTRY_USERNAME=your-github-username
   export REGISTRY_PASSWORD=ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
   ```

## Запуск

### Локальный запуск
```bash
# Сборка
go build -o agent-service ./cmd/server

# Запуск
./agent-service
```

### Docker Compose
```bash
# Создайте .env файл с настройками registry
docker-compose up --build
```

### Docker контейнер
```bash
docker run -d \
  -p 8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e REGISTRY_SERVER=ghcr.io \
  -e REGISTRY_USERNAME=your-username \
  -e REGISTRY_PASSWORD=your-token \
  agent-svc
```

Примечание: сервис поддерживает Prometheus-метрики на `/metrics`.
