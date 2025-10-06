# Agent Service

Описание: сервис для управления контейнерами-агентами с поддержкой реального Docker API.

## 🚀 Интеграция с Orchestrator API

**НОВОЕ!** AgentManager интегрируется с внешним Orchestrator API.

📖 **Начните здесь:** [QUICK_START.md](QUICK_START.md)

**Ключевые изменения:**
- ✅ Резервирование задач с `agent_uuid`
- ✅ SSH ключи на уровне **проектов** (генерируются оркестратором, передаются агентам)
- ✅ Передача `TASK_ID` и `AGENT_UUID` в контейнер

**Документация:**
- [QUICK_START.md](QUICK_START.md) - быстрый старт (5 мин)
- [INTEGRATION_CHANGES_SUMMARY.md](INTEGRATION_CHANGES_SUMMARY.md) - сводка изменений
- [docs/INDEX.md](docs/INDEX.md) - полный индекс документации

## Возможности

- **Реализация интерфейса AgentOrchestratorInterface**
- **Интеграция с образом ghcr.io/vasenin26/agentmodule**
- **Интеграция с внешним Orchestrator API** ⭐ **NEW**
- **SSH ключи на уровне проектов** ⭐ **NEW**
- **Автоматическая генерация SSH ключей** (оркестратором)
- **Безопасное хранение SSH ключей в файловой системе**
- **Управление Docker контейнерами**
- **Аутентификация в Docker Registry**
- **Prometheus метрики**
- **Логирование операций**

## SSH Ключи

### ⭐ Новая архитектура: SSH ключи на уровне проектов

**Оркестратор генерирует SSH ключи на уровне ПРОЕКТА:**

- **Генерация**: Оркестратор генерирует ключи **ОДИН РАЗ** для проекта
- **Хранение**: `keys/projects/{projectId}_private.pem` и `_public.pub`
- **Передача**: Приватный ключ проекта **передается ВСЕМ агентам** этого проекта
- **Регистрация**: Публичный ключ → External API → GitHub/GitLab
- **Шифрование**: RSA 2048-bit

**Преимущества:**
- ✅ Один ключ на проект (вместо ключа для каждого агента)
- ✅ Масштабируемость - 1000 агентов = 1 ключ в GitHub
- ✅ Простая ротация ключей
- ✅ Все агенты проекта используют один ключ для git-операций

**Подробнее:** [docs/ssh-keys-architecture.md](docs/ssh-keys-architecture.md)

### Хранение SSH ключей

SSH ключи проектов сохраняются в файловой системе:
- **Приватные ключи**: `keys/projects/{projectId}_private.pem` (права 600)
- **Публичные ключи**: `keys/projects/{projectId}_public.pub` (права 644)
- **Генерируются**: Оркестратором при получении первой задачи проекта
- **Передаются**: Всем агентам проекта через `SSH_PRIVATE_KEY`
- **Регистрируются**: Публичный ключ в External API → GitHub
- **Безопасность**: Директория создается с правами 700

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

## Подготовка сервера для деплоя (prod)

1. Установить Docker и Docker Compose
   ```bash
   sudo apt update && sudo apt install -y ca-certificates curl gnupg
   sudo install -m 0755 -d /etc/apt/keyrings
   curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
   echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
   sudo apt update
   sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
   sudo usermod -aG docker $USER
   newgrp docker
   ```

2. Подготовить директорию деплоя и файлы
   ```bash
   sudo mkdir -p /opt/agentmanager
   sudo chown -R $USER:$USER /opt/agentmanager
   # Примечание: workflow CI/CD может сам загрузить docker-compose.prod.yaml и создать .env во время деплоя
   ```

3. Настроить доступ к GHCR (если образ приватный)
   ```bash
   docker login ghcr.io
   # введите GitHub username и PAT с правами read:packages
   ```

4. Запуск/обновление приложения
   ```bash
   cd /opt/agentmanager
   docker compose -f docker-compose.prod.yaml --env-file .env pull
   docker compose -f docker-compose.prod.yaml --env-file .env up -d
   ```

5. (Опционально) Автозапуск через systemd
   ```bash
   sudo tee /etc/systemd/system/agentmanager.service >/dev/null <<'EOF'
   [Unit]
   Description=agentmanager via Docker Compose
   Requires=docker.service
   After=docker.service

   [Service]
   Type=oneshot
   WorkingDirectory=/opt/agentmanager
   ExecStart=/usr/bin/docker compose -f docker-compose.prod.yaml --env-file .env up -d
   ExecStop=/usr/bin/docker compose -f docker-compose.prod.yaml --env-file .env down
   RemainAfterExit=yes
   TimeoutStartSec=0

   [Install]
   WantedBy=multi-user.target
   EOF
   sudo systemctl daemon-reload
   sudo systemctl enable --now agentmanager
   ```

### Пример .env (prod)
```bash
# HTTP
HTTP_PORT=8080

# Docker/Registry (для внутренней работы сервиса)
REGISTRY_SERVER=ghcr.io
REGISTRY_USERNAME=
REGISTRY_PASSWORD=

# Agent config (по необходимости)
API_HOST=
OPENAI_MODEL=
OPENAI_API_KEY=
GIT_USER_NAME=
GIT_USER_EMAIL=

# SSH keys внутри контейнера (используется named volume)
SSH_KEYS_DIR=/app/keys

# Тег образа, деплой по релизным тегам
IMAGE_TAG=latest
```

## CI/CD: Автодеплой из GitHub Actions

Деплой запускается автоматически при пуше тега формата `vX.X.X` (например, `v1.2.3`). Workflow:

- Собирает образ из `Dockerfile`
- Пушит образы в GHCR: `latest`, `SHA`, и версионный тег (`v1.2.3`)
- По SSH заходит на сервер, готовит директорию, загружает `docker-compose.prod.yaml`, создаёт/обновляет `.env`
- Устанавливает `IMAGE_TAG` из тега релиза и выполняет `docker compose pull && up -d`

Требуемые Secrets (Repository → Settings → Secrets and variables → Actions):

- `SSH_HOST` — адрес сервера
- `SSH_USER` — пользователь
- `SSH_KEY` — приватный SSH ключ (PEM)
- `SSH_PORT` — порт (опционально, по умолчанию 22)
- `REMOTE_DIR` — путь деплоя (например, `/opt/agentmanager`)
- `ENV_FILE_CONTENTS` — содержимое `.env` (опционально; если задано, будет записано при деплое)
- `GHCR_USERNAME`, `GHCR_TOKEN` — нужны, если образ приватный (pull на сервере)

Выпуск релиза:

```bash
git tag v1.2.3
git push origin v1.2.3
```

Проверка статуса на сервере:

```bash
docker compose -f /opt/agentmanager/docker-compose.prod.yaml --env-file /opt/agentmanager/.env ps
curl -fsSL http://localhost:8080/metrics | head
```

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
