# Project SSH Keys Management

## Обзор

Оркестратор управляет SSH ключами на уровне проектов. Каждый проект имеет свою пару SSH ключей, которая используется агентами для доступа к Git репозиториям проекта.

## Архитектура

### Хранение ключей

```
keys/
├── projects/
│   ├── project-123_private.pem  # Приватный ключ проекта 123
│   ├── project-123_public.pub   # Публичный ключ проекта 123
│   ├── project-456_private.pem
│   └── project-456_public.pub
└── {agentID}_private.pem         # Ключи агентов (старая логика)
    {agentID}_public.pub
```

### Два типа SSH ключей

1. **Project SSH Keys** - Привязаны к проекту
   - Хранятся в `keys/projects/`
   - Используются для клонирования репозитория проекта
   - Передаются через `PROJECT_SSH_PRIVATE_KEY`

2. **Agent SSH Keys** - Привязаны к агенту
   - Хранятся в `keys/`
   - Используются для Git операций агента
   - Передаются через `SSH_PRIVATE_KEY`

## Логика работы

### При получении задачи

```mermaid
graph TD
    A[Получить задачу с project_id и public_key] --> B{Ключи проекта существуют?}
    B -->|Да| C{public_key передан?}
    B -->|Нет| F[Генерировать новую пару]
    C -->|Да| D{Ключ валиден?}
    C -->|Нет| E[Использовать существующие ключи]
    D -->|Да| E
    D -->|Нет| F
    F --> G[Отправить public_key в API]
    G --> H[Запустить агента с ключами]
    E --> H
```

### Подробный процесс

#### 1. Проверка существования ключей

```go
if storage.ProjectKeyPairExists(projectID) {
    // Ключи существуют - проверить валидность
} else {
    // Генерировать новую пару
}
```

#### 2. Валидация публичного ключа

Если в задаче передан `public_key`:
- Получить сохраненную пару ключей
- Сравнить публичные ключи
- Если не совпадают - генерировать новую пару

```go
isValid, err := storage.ValidateProjectPublicKey(projectID, publicKey)
if !isValid {
    // Генерировать новую пару
}
```

#### 3. Генерация новой пары

- Создать RSA ключи (2048 бит)
- Сохранить в `keys/projects/`
- Отправить публичный ключ в API через `PUT /projects/{projectID}/key`

## API Endpoints

### GET /tasks/next

Получение задачи с информацией о проекте:

**Response:**
```json
{
  "id": "task-123",
  "context_id": "context-456",
  "timeout": 300,
  "project_id": "project-789",
  "public_key": "ssh-rsa AAAAB3NzaC1yc2EA..."  // может быть null
}
```

### PUT /projects/{projectID}/key

Обновление публичного ключа проекта:

**Request:**
```json
{
  "public_key": "ssh-rsa AAAAB3NzaC1yc2EA..."
}
```

**Response:** 200 OK или 204 No Content

## Environment Variables

Агент получает оба ключа:

```bash
# Ключ проекта (для клонирования репозитория)
PROJECT_SSH_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----
...
-----END RSA PRIVATE KEY-----

# Ключ агента (для Git операций)
SSH_PRIVATE_KEY=-----BEGIN RSA PRIVATE KEY-----
...
-----END RSA PRIVATE KEY-----
```

## Сценарии использования

### Сценарий 1: Первый запуск проекта

1. Оркестратор получает задачу с `project_id=project-123`, `public_key=null`
2. Ключей для проекта нет
3. Генерируется новая пара ключей
4. Публичный ключ отправляется в API
5. Агент запускается с приватным ключом

### Сценарий 2: Повторный запуск проекта

1. Оркестратор получает задачу с `project_id=project-123`, `public_key=<existing>`
2. Ключи проекта существуют
3. Публичный ключ валиден
4. Агент запускается с существующим приватным ключом

### Сценарий 3: Несоответствие ключей

1. Оркестратор получает задачу с `project_id=project-123`, `public_key=<new>`
2. Ключи проекта существуют, но публичный ключ не совпадает
3. Генерируется новая пара ключей
4. Новый публичный ключ отправляется в API
5. Агент запускается с новым приватным ключом

### Сценарий 4: API не передает публичный ключ

1. Оркестратор получает задачу с `project_id=project-123`, `public_key=null`
2. Ключи проекта существуют
3. Валидация пропускается (нечего проверять)
4. Агент запускается с существующим приватным ключом

## Безопасность

### Права доступа к файлам

- Приватные ключи: `0600` (только владелец может читать/писать)
- Публичные ключи: `0644` (владелец может писать, все могут читать)
- Директория projects: `0700` (только владелец может входить)

### Хранение ключей

- Ключи хранятся в файловой системе
- Монтируются через Docker volume `ssh_keys`
- Приватные ключи не логируются
- Публичные ключи безопасно передаются в API

### Рекомендации

1. **Backup ключей**: Регулярно создавайте резервные копии `keys/projects/`
2. **Ротация**: Периодически обновляйте ключи проектов
3. **Monitoring**: Отслеживайте несанкционированный доступ к файлам ключей
4. **Audit**: Логируйте генерацию и изменение ключей

## Troubleshooting

### Ключи не генерируются

Проверьте:
- Права на запись в директорию `keys/projects/`
- Логи оркестратора для ошибок генерации
- Доступность файловой системы

### Публичный ключ не отправляется в API

Проверьте:
- `TASK_API_URL` и `TASK_API_TOKEN` корректны
- API доступен
- Endpoint `/projects/{projectID}/key` существует

### Агент не может клонировать репозиторий

Проверьте:
- `PROJECT_SSH_PRIVATE_KEY` присутствует в переменных окружения агента
- Публичный ключ добавлен в Git сервис (GitHub, GitLab)
- Формат ключа корректен

### Проверка ключей

```bash
# Список ключей проектов
ls -la keys/projects/

# Проверка формата публичного ключа
ssh-keygen -l -f keys/projects/project-123_public.pub

# Проверка приватного ключа
ssh-keygen -y -f keys/projects/project-123_private.pem

# Проверка переменных окружения агента
docker inspect <container-id> | jq '.[0].Config.Env' | grep PROJECT_SSH
```

## Логирование

Оркестратор логирует следующие события:

```
INFO  Project keys not found, generating new key pair  projectID=project-123
INFO  Successfully registered project public key       projectID=project-123
WARN  Public key mismatch, generating new key pair     projectID=project-123
ERROR Failed to validate public key                    projectID=project-123 error=...
ERROR Failed to update project public key in API       projectID=project-123 error=...
```

## Миграция существующих проектов

Если у вас уже есть проекты с ключами:

1. Создайте директорию `keys/projects/`
2. Скопируйте существующие ключи в нужный формат:
   ```bash
   cp existing-key.pem keys/projects/project-123_private.pem
   cp existing-key.pub keys/projects/project-123_public.pub
   ```
3. Установите корректные права доступа:
   ```bash
   chmod 600 keys/projects/*_private.pem
   chmod 644 keys/projects/*_public.pub
   ```
4. Перезапустите оркестратор

## Мониторинг

### Метрики

В будущих версиях планируются метрики:
- `orchestrator_project_keys_generated_total` - Счетчик сгенерированных ключей
- `orchestrator_project_keys_validated_total` - Счетчик валидаций
- `orchestrator_project_keys_mismatched_total` - Счетчик несовпадений

### Логи

Включите DEBUG уровень для детального логирования операций с ключами.

## Примеры кода

### Проверка валидности ключа

```go
isValid, err := sshStorage.ValidateProjectPublicKey("project-123", publicKey)
if err != nil {
    log.Error("Validation failed", zap.Error(err))
}
if !isValid {
    log.Warn("Key mismatch")
}
```

### Генерация новой пары

```go
keyPair, err := sshStorage.GenerateAndStoreProjectKeyPair("project-123")
if err != nil {
    return err
}
log.Info("Generated keys", zap.String("publicKey", keyPair.PublicKey))
```

### Получение существующих ключей

```go
keyPair, err := sshStorage.GetProjectKeyPair("project-123")
if err != nil {
    log.Error("Keys not found", zap.Error(err))
}
```

