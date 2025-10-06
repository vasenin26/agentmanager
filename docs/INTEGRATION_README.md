# Интеграция AgentManager с Orchestrator API

## 📋 Документы

| Документ | Назначение |
|----------|------------|
| **[integration-summary.md](./integration-summary.md)** | ⚡ Краткая сводка изменений (читать первым!) |
| **[integration-plan.md](./integration-plan.md)** | 📖 Полный технический план реализации |
| **[architecture-comparison.md](./architecture-comparison.md)** | 🎨 Визуальное сравнение архитектур до/после |
| **[task-api-specification.md](./task-api-specification.md)** | 📄 Спецификация Task API |

## 🎯 Цель

Интеграция AgentManager (Go оркестратор) с внешним Orchestrator API (Laravel/PHP), реализованным в проекте `docmodule/plane`.

## 🔑 Ключевые изменения

### 1. Резервирование с `agent_uuid`

**До:**
```go
ReserveTask(taskID, reserveSeconds)
```

**После:**
```go
ReserveTask(taskID, reserveSeconds, agentUUID)
```

### 2. SSH ключи на уровне проектов

**До:** Ключи для каждого агента  
**После:** Общие ключи для всех агентов проекта

### 3. Передача контекста агенту

**Новые переменные окружения:**
- `TASK_ID` - ID задачи
- `AGENT_UUID` - UUID воркера

## 📊 Workflow

```
┌─────────────────────────────────────────────────────────────┐
│                    AgentManager                             │
│                                                             │
│  1. GET /orchestrator/tasks/next                            │
│     ↓ TaskDTO                                               │
│                                                             │
│  2. Generate agentUUID                                      │
│                                                             │
│  3. POST /orchestrator/tasks/{id}/reserve                   │
│     Body: {reserve_seconds, agent_uuid}                     │
│                                                             │
│  4. Check/Generate Project SSH keys                         │
│                                                             │
│  5. Start Docker container                                  │
│     Env: TASK_ID, AGENT_UUID, API_TOKEN                     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────────┐
│              Docker Container (Agent)                       │
│                                                             │
│  6. POST /api/agent/task                                    │
│     Body: {agent_uuid}                                      │
│     ↓ Full task data (handler, options, etc.)               │
│                                                             │
│  7. Execute task                                            │
│                                                             │
│  8. PUT /api/agent/task/{id}                                │
│     Body: {completed: true}                                 │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## 🚀 Быстрый старт

### Шаг 1: Изучение документации

```bash
# Читаем краткую сводку (5 минут)
cat docs/integration-summary.md

# Изучаем визуальные диаграммы (10 минут)
cat docs/architecture-comparison.md

# Читаем полный план (30 минут)
cat docs/integration-plan.md
```

### Шаг 2: Создание feature branch

```bash
git checkout -b feature/orchestrator-api-integration
```

### Шаг 3: Реализация изменений

**Порядок реализации:**

1. **Создать `internal/ssh/project_keys.go`** (30 мин)
   - Структура `ProjectKeyManager`
   - Методы генерации и валидации ключей

2. **Обновить `internal/external/task_client.go`** (15 мин)
   - Добавить параметр `agentUUID` в `ReserveTask()`
   - Обновить payload запроса

3. **Обновить `internal/service/orchestrator_service.go`** (45 мин)
   - Интегрировать `ProjectKeyManager`
   - Генерировать `agentUUID` в `processTask()`
   - Обработка конфликтов резервирования

4. **Обновить `internal/service/agent_service.go`** (20 мин)
   - Добавить параметр `agentUUID` в `StartAgentForTask()`
   - Передать `TASK_ID` и `AGENT_UUID` в контейнер

5. **Добавить метрики** (10 мин)
   - `TaskReservationConflictsTotal`

### Шаг 4: Тестирование

```bash
# Unit тесты
go test ./internal/ssh/... -v
go test ./internal/external/... -v
go test ./internal/service/... -v

# Integration тесты с mock API
# Терминал 1: Запустить mock API
python tests/mock/orchestrator_api.py

# Терминал 2: Запустить AgentManager
TASK_API_URL=http://localhost:9000/api/v1/orchestrator \
TASK_API_TOKEN=test-token \
./bin/agentmanager
```

### Шаг 5: Code Review и Деплой

```bash
# Commit изменений
git add .
git commit -m "feat: integrate with Orchestrator API

- Add agent_uuid to task reservation
- Implement project-level SSH keys
- Pass TASK_ID and AGENT_UUID to agent container
- Handle reservation conflicts (409)
- Add TaskReservationConflictsTotal metric

Refs: docs/integration-plan.md"

# Push и создание PR
git push origin feature/orchestrator-api-integration

# После review - merge и release
git tag v2.0.0
git push origin v2.0.0
```

## 📁 Структура изменений

### Новые файлы

```
internal/ssh/
├── project_keys.go          # Управление SSH ключами проектов
└── project_keys_test.go     # Тесты

tests/
├── integration/
│   └── orchestrator_api_test.go  # Интеграционные тесты
└── mock/
    └── orchestrator_api.py       # Mock API для разработки

docs/
├── integration-summary.md        # Краткая сводка ⭐
├── integration-plan.md          # Полный план ⭐
├── architecture-comparison.md   # Диаграммы ⭐
└── ssh-keys-architecture.md     # Архитектура SSH ключей
```

### Измененные файлы

```
internal/
├── external/
│   └── task_client.go           # +agent_uuid в ReserveTask()
├── service/
│   ├── orchestrator_service.go  # +ProjectKeyManager, обработка конфликтов
│   └── agent_service.go         # +TASK_ID, +AGENT_UUID в контейнер
├── models/
│   └── task.go                  # +AgentUUID поле
└── metrics/
    └── metrics.go               # +TaskReservationConflictsTotal

docs/
├── task-api-specification.md    # Обновить workflow
└── README.md                    # Добавить SSH ключи проектов
```

## 🧪 Тестовые сценарии

### Сценарий 1: Нормальный workflow

```bash
# Mock API возвращает задачу
curl http://localhost:9000/api/v1/orchestrator/tasks/next

# AgentManager резервирует задачу
curl -X POST http://localhost:9000/api/v1/orchestrator/tasks/task-1/reserve \
  -H "Content-Type: application/json" \
  -d '{"reserve_seconds": 300, "agent_uuid": "uuid-123"}'

# Проверяем логи AgentManager
# ✅ Task reserved successfully
# ✅ Project keys validated/generated
# ✅ Agent container started
```

### Сценарий 2: Конфликт резервирования

```bash
# Оркестратор A резервирует задачу
curl -X POST http://localhost:9000/api/v1/orchestrator/tasks/task-1/reserve \
  -H "Content-Type: application/json" \
  -d '{"reserve_seconds": 300, "agent_uuid": "uuid-A"}'

# Оркестратор B пытается зарезервировать ту же задачу
curl -X POST http://localhost:9000/api/v1/orchestrator/tasks/task-1/reserve \
  -H "Content-Type: application/json" \
  -d '{"reserve_seconds": 300, "agent_uuid": "uuid-B"}'

# Ожидаемый результат:
# HTTP 409 Conflict
# {"error": "Task already reserved", "reserved_until": "..."}

# Проверяем метрики
curl http://localhost:8080/metrics | grep task_reservation_conflicts_total
# task_reservation_conflicts_total 1
```

### Сценарий 3: SSH ключи проекта

```bash
# Первая задача проекта - генерация ключей
# Проверяем создание файлов
ls -la keys/projects/
# -rw------- project-100_private.pem
# -rw-r--r-- project-100_public.pub

# Проверяем отправку публичного ключа в API
# Лог: "Successfully registered project public key"

# Вторая задача того же проекта - переиспользование ключей
# Лог: "Project keys found, validating..."
# Лог: "Public key valid"
```

## 📈 Метрики для мониторинга

### Grafana Dashboard

```promql
# Конфликты резервирования за последние 5 минут
rate(task_reservation_conflicts_total[5m])

# Алерт: слишком много конфликтов
rate(task_reservation_conflicts_total[5m]) > 0.1

# Успешность резервирования
(tasks_processed_total / (tasks_processed_total + task_reservation_conflicts_total)) * 100
```

## 🐛 Troubleshooting

### Проблема: Задачи не резервируются

**Симптомы:**
```
Error: Failed to reserve task: unexpected status code: 400
```

**Решение:**
```bash
# Проверить payload запроса
# Должен содержать agent_uuid!

# Проверить External API endpoint
curl -X POST $TASK_API_URL/tasks/task-1/reserve \
  -H "Authorization: Bearer $TASK_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reserve_seconds": 10, "agent_uuid": "test-uuid"}'
```

### Проблема: SSH ключи не генерируются

**Симптомы:**
```
Error: Failed to generate project key pair: permission denied
```

**Решение:**
```bash
# Проверить права на директорию
ls -la keys/
# drwx------ projects/

# Создать директорию вручную
mkdir -p keys/projects
chmod 700 keys/projects
```

### Проблема: Агент не получает задачу

**Симптомы:**
Агент запускается, но не начинает работу

**Решение:**
```bash
# Проверить переменные окружения в контейнере
docker exec <container_id> env | grep -E "TASK_ID|AGENT_UUID"
# TASK_ID=task-123
# AGENT_UUID=uuid-456

# Проверить, вызывает ли агент POST /api/agent/task
# Лог агента должен содержать: "Fetching task details..."
```

## 📞 Поддержка

### Документация

- **External API спецификация:** `/docmodule/plane/orchestrator-api-technical-plan.md`
- **FAQ External API:** `/docmodule/docs/ORCHESTRATOR_API_FAQ.md`
- **Текущая Task API spec:** `docs/task-api-specification.md`

### Полезные команды

```bash
# Проверить статус миграции
git status

# Запустить все тесты
go test ./... -v

# Проверить покрытие тестами
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Проверить линтером
golangci-lint run

# Билд для production
make build

# Запуск с debug логами
LOG_LEVEL=debug ./bin/agentmanager
```

## ✅ Checklist перед коммитом

- [ ] Все unit тесты проходят
- [ ] Integration тесты с mock API проходят
- [ ] Документация обновлена
- [ ] Линтер не выдает ошибок
- [ ] Метрики работают
- [ ] SSH ключи создаются с правильными правами (600/644)
- [ ] Конфликты резервирования обрабатываются корректно
- [ ] Логирование информативное

## 🎓 Дополнительное обучение

### Понимание External API

1. Прочитать `docmodule/plane/orchestrator-api-technical-plan.md`
2. Изучить FAQ: `docmodule/docs/ORCHESTRATOR_API_FAQ.md`
3. Посмотреть реализацию: `docmodule/app/app/Services/OrchestratorTaskService.php`

### Понимание агента

1. Агент - это Docker контейнер с образом `ghcr.io/vasenin26/agentmodule`
2. Агент получает переменные окружения от AgentManager
3. Агент сам вызывает Task API для получения деталей задачи
4. Агент самостоятельно отмечает задачу как выполненную

---

**Автор плана:** AI Assistant  
**Дата создания:** 2025-10-06  
**Версия:** 1.0

**Статус:** ✅ Готов к реализации

