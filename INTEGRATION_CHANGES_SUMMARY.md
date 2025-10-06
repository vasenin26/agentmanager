# Сводка изменений: Интеграция с Orchestrator API

## 📚 Документация создана

| Файл | Описание | Приоритет |
|------|----------|-----------|
| **[docs/INTEGRATION_README.md](docs/INTEGRATION_README.md)** | Главный файл - начните с него! | ⭐⭐⭐ |
| **[docs/integration-summary.md](docs/integration-summary.md)** | Краткая сводка изменений | ⭐⭐⭐ |
| **[docs/integration-plan.md](docs/integration-plan.md)** | Полный технический план (1250+ строк) | ⭐⭐ |
| **[docs/architecture-comparison.md](docs/architecture-comparison.md)** | Визуальные диаграммы до/после | ⭐⭐ |
| **[docs/ssh-keys-architecture.md](docs/ssh-keys-architecture.md)** | Детальное описание SSH ключей | ⭐ |

## 🎯 Основные изменения

### 1️⃣ Резервирование с `agent_uuid`

**Было:**
```go
ReserveTask(taskID string, reserveSeconds int)
```

**Стало:**
```go
ReserveTask(taskID string, reserveSeconds int, agentUUID string)
```

**Payload запроса:**
```json
{
  "reserve_seconds": 300,
  "agent_uuid": "550e8400-e29b-41d4-a716-446655440000"
}
```

### 2️⃣ SSH ключи на уровне проектов

**❗ ВАЖНО:** Ключи генерируются **оркестратором** и **выдаются агентам**

#### Было (agent-level):
```
Каждый агент = уникальный SSH ключ
100 агентов → 100 ключей в GitHub
```

#### Стало (project-level):
```
Один проект = один SSH ключ (генерируется оркестратором)
100 агентов одного проекта → 1 ключ в GitHub

Workflow:
1. Оркестратор генерирует ключи ОДИН РАЗ для проекта
2. Публичный ключ → External API → GitHub/GitLab  
3. Приватный ключ → передается ВСЕМ агентам проекта
4. Все агенты используют ОДИНАКОВЫЙ ключ для git-операций
```

### 3️⃣ Передача контекста агенту

**Новые переменные окружения:**
```bash
TASK_ID=task-123              # ID задачи
AGENT_UUID=uuid-456           # UUID воркера
SSH_PRIVATE_KEY=<project-key> # ← Ключ ПРОЕКТА, не агента!
```

## 📁 Файлы для изменения

### ✅ Создать новые файлы

```
internal/ssh/
├── project_keys.go          # Управление SSH ключами проектов
└── project_keys_test.go     # Unit тесты

tests/
├── integration/
│   └── orchestrator_api_test.go  # Integration тесты
└── mock/
    └── orchestrator_api.py       # Mock API для разработки
```

### ✏️ Изменить существующие файлы

```
internal/external/
└── task_client.go           # +agent_uuid в ReserveTask()

internal/service/
├── orchestrator_service.go  # +ProjectKeyManager, генерация ключей
└── agent_service.go         # +TASK_ID, +AGENT_UUID в контейнер

internal/models/
└── task.go                  # +AgentUUID поле

internal/metrics/
└── metrics.go               # +TaskReservationConflictsTotal
```

## 🔑 Ключевая концепция SSH ключей

### Кто генерирует ключи?
**Оркестратор (AgentManager)** - не агенты!

### Когда генерируются ключи?
**Один раз** при получении первой задачи нового проекта

### Кто получает ключи?
**Все агенты проекта** получают **один и тот же** приватный ключ проекта

### Пример workflow:

```
┌─────────────────────────────────────────────────────────┐
│            Orchestrator (AgentManager)                  │
│                                                         │
│  Task received: project-100                             │
│         ↓                                               │
│  Check: keys/projects/project-100_*.pem exists?         │
│         ↓ NO                                            │
│  Generate SSH key pair (ONE TIME)                       │
│         ├─ Save: project-100_private.pem (local)        │
│         └─ Send: project-100_public.pub → External API  │
│                                                         │
└───────────────────┬─────────────────────────────────────┘
                    │
         ┌──────────┴──────────┬──────────────┐
         ▼                     ▼              ▼
    ┌────────┐            ┌────────┐     ┌────────┐
    │Agent #1│            │Agent #2│     │Agent #3│
    │        │            │        │     │        │
    │SSH_KEY=│            │SSH_KEY=│     │SSH_KEY=│
    │<same>  │            │<same>  │     │<same>  │
    └────────┘            └────────┘     └────────┘
         │                     │              │
         └─────────────────────┴──────────────┘
                         │
                         ▼
                  ┌────────────┐
                  │   GitHub   │
                  │            │
                  │ Deploy key:│
                  │ public_key │
                  └────────────┘
```

## 🚀 Порядок реализации

### Этап 1: SSH ключи проектов (60 мин)
1. Создать `internal/ssh/project_keys.go`
   - `ProjectKeyManager` структура
   - `GenerateKeyPair()` - генерация ключей проекта
   - `GetKeyPair()` - получение существующих ключей
   - `KeyPairExists()` - проверка существования
   - `ValidatePublicKey()` - валидация

### Этап 2: Резервирование с agent_uuid (30 мин)
2. Обновить `internal/external/task_client.go`
   - Добавить параметр `agentUUID` в `ReserveTask()`
   - Обновить payload: `{"reserve_seconds", "agent_uuid"}`
   - Обработать 409 Conflict

### Этап 3: Интеграция в оркестратор (45 мин)
3. Обновить `internal/service/orchestrator_service.go`
   - Добавить `ProjectKeyManager` в структуру
   - Реализовать `validateAndPrepareProjectKey()`
   - Реализовать `generateAndRegisterProjectKey()`
   - Генерировать `agentUUID` в `processTask()`
   - Обработать конфликты резервирования

### Этап 4: Передача в контейнер (20 мин)
4. Обновить `internal/service/agent_service.go`
   - Добавить параметр `agentUUID`
   - Добавить env: `TASK_ID`, `AGENT_UUID`
   - Передать `SSH_PRIVATE_KEY` (ключ проекта!)

### Этап 5: Метрики (10 мин)
5. Добавить `internal/metrics/metrics.go`
   - `TaskReservationConflictsTotal`

### Этап 6: Тестирование (2-3 часа)
6. Unit тесты
7. Integration тесты с mock API
8. Ручное тестирование

**Общее время:** 5-7 дней (с документацией и review)

## ✅ Checklist перед коммитом

- [ ] Все файлы созданы согласно плану
- [ ] Unit тесты проходят
- [ ] Integration тесты с mock API проходят
- [ ] Линтер не выдает ошибок
- [ ] SSH ключи создаются с правами 600/644
- [ ] Публичный ключ отправляется в External API
- [ ] Приватный ключ передается агентам через `SSH_PRIVATE_KEY`
- [ ] Конфликты резервирования обрабатываются (409)
- [ ] Метрики работают
- [ ] Документация обновлена

## 📖 Как читать документацию

### Для быстрого ознакомления (15 минут):
1. Читайте **[docs/INTEGRATION_README.md](docs/INTEGRATION_README.md)**
2. Просмотрите **[docs/integration-summary.md](docs/integration-summary.md)**

### Для понимания архитектуры (1 час):
3. Изучите **[docs/architecture-comparison.md](docs/architecture-comparison.md)** - визуальные диаграммы
4. Прочитайте **[docs/ssh-keys-architecture.md](docs/ssh-keys-architecture.md)** - детали SSH ключей

### Для реализации (полный день):
5. Следуйте **[docs/integration-plan.md](docs/integration-plan.md)** - пошаговый план

## 🎓 Ключевые понятия

### agent_uuid
UUID воркера, генерируется оркестратором для каждого резервирования задачи.  
**Назначение:** Идентификация воркера в External API.

### Project SSH Keys
SSH ключи на уровне проекта (не агента!).  
**Генерируются:** Оркестратором один раз для проекта.  
**Используются:** Всеми агентами этого проекта.

### Task Reservation
Двухфазное резервирование:
1. **GET /tasks/next** - получение информации о задаче
2. **POST /tasks/{id}/reserve** - резервирование с `agent_uuid` и `reserve_seconds`

### Reserve Seconds
Прогноз времени до запуска агента:
- Без контекста: 10 сек
- С контекстом (свободен): 10 сек
- С контекстом (занят): 300 сек

## 🔍 Тестирование

### Mock API для разработки

```bash
# Терминал 1: Запустить mock API
cd tests/mock
python orchestrator_api.py

# Терминал 2: Запустить AgentManager
TASK_API_URL=http://localhost:9000/api/v1/orchestrator \
TASK_API_TOKEN=test-token \
./bin/agentmanager
```

### Проверить SSH ключи

```bash
# Проверить генерацию ключей
ls -la keys/projects/
# -rw------- project-100_private.pem
# -rw-r--r-- project-100_public.pub

# Проверить формат публичного ключа
cat keys/projects/project-100_public.pub
# ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ...
```

### Проверить метрики

```bash
curl http://localhost:8080/metrics | grep task_reservation
# task_reservation_conflicts_total 0
```

## 📞 Дополнительные ресурсы

### Внешняя документация (docmodule/plane)
- `docmodule/plane/orchestrator-api-technical-plan.md` - техплан External API
- `docmodule/docs/ORCHESTRATOR_API_FAQ.md` - FAQ по External API
- `docmodule/ORCHESTRATOR_API_IMPLEMENTATION_SUMMARY.md` - сводка реализации

### Внутренняя документация (agentmanager)
- `docs/task-api-specification.md` - спецификация Task API
- `docs/orchestrator-configuration.md` - конфигурация оркестратора
- `docs/task-reservation-pattern.md` - паттерн резервирования
- `README.md` - общая документация

## 🎉 Итого

Все документы созданы и готовы к использованию!

**Начните с:** [docs/INTEGRATION_README.md](docs/INTEGRATION_README.md)

**Следующие шаги:**
1. Изучить документацию (1-2 часа)
2. Создать feature branch
3. Реализовать изменения по плану (5-7 дней)
4. Тестирование
5. Code review
6. Деплой

---

**Автор:** AI Assistant  
**Дата:** 2025-10-06  
**Версия:** 1.0  
**Статус:** ✅ Готово к реализации

