# Сравнение архитектур: До и После интеграции

## Текущая архитектура (До изменений)

```
┌─────────────────────────────────────────────────────────────────┐
│                      AgentManager (Go)                          │
│                                                                 │
│  ┌──────────────┐                                               │
│  │OrchestratorSvc│                                              │
│  └───────┬──────┘                                               │
│          │                                                      │
│          │ 1. FetchTask()                                       │
│          ├──────────────────────────────────────────────┐       │
│          │                                              │       │
│          │ 2. ReserveTask(taskID, reserveSeconds)      │       │
│          │    ❌ Нет agent_uuid!                        │       │
│          ├──────────────────────────────────────────────┤       │
│          │                                              │       │
│  ┌───────▼────────┐                            ┌────────▼─────┐ │
│  │  TaskClient    │───────────────────────────▶│ External API │ │
│  └────────────────┘  HTTP JSON                 └──────────────┘ │
│          │                                                      │
│          │ 3. StartAgentForTask()                               │
│          ▼                                                      │
│  ┌──────────────┐                                               │
│  │ AgentService │                                               │
│  └───────┬──────┘                                               │
│          │                                                      │
│          │ 4. Запуск Docker контейнера                          │
│          ▼                                                      │
│  ┌─────────────────────────────────────────┐                    │
│  │       Docker Container (Agent)          │                    │
│  │                                         │                    │
│  │  Env:                                   │                    │
│  │  - AGENT_ID                             │                    │
│  │  - API_TOKEN                            │                    │
│  │  - SSH_PRIVATE_KEY (agent key)          │                    │
│  │  ❌ Нет TASK_ID                          │                    │
│  │  ❌ Нет AGENT_UUID                       │                    │
│  └─────────────────────────────────────────┘                    │
│          │                                                      │
│          │ ❓ Как агент получает задачу?                         │
│          └─────────────────────────────────────────────────────▶│
│                                                                 │
└─────────────────────────────────────────────────────────────────┘

Проблемы:
1. ❌ External API не получает agent_uuid при резервировании
2. ❌ Агент не знает TASK_ID для получения деталей задачи
3. ❌ SSH ключи на уровне агента, а не проекта
4. ❌ Нет механизма получения полных данных задачи агентом
```

## Новая архитектура (После изменений)

```
┌────────────────────────────────────────────────────────────────────────┐
│                        AgentManager (Go)                               │
│                                                                        │
│  ┌──────────────┐                                                      │
│  │OrchestratorSvc│                                                     │
│  └───────┬──────┘                                                      │
│          │                                                             │
│          │ 1. FetchTask()                                              │
│          ├─────────────────────────────────────────────────┐           │
│          │                                                 │           │
│          │ 2. Generate agent_uuid = uuid.New()            │           │
│          │                                                 │           │
│          │ 3. ReserveTask(taskID, reserveSeconds, agentUUID)          │
│          │    ✅ С agent_uuid!                              │           │
│          ├─────────────────────────────────────────────────┤           │
│          │                                                 │           │
│  ┌───────▼────────┐                               ┌────────▼─────┐     │
│  │  TaskClient    │──────────────────────────────▶│ External API │     │
│  └────────────────┘  HTTP JSON                    │ (Laravel)    │     │
│          │           Body: {                      └──────────────┘     │
│          │             reserve_seconds: 300,                           │
│          │             agent_uuid: "uuid-123"                          │
│          │           }                                                 │
│          │                                                             │
│          │ 4. ValidateProjectKeys(task.ProjectID)                      │
│          ▼                                                             │
│  ┌─────────────────────┐                                               │
│  │ ProjectKeyManager   │                                               │
│  └──────────┬──────────┘                                               │
│             │                                                          │
│             │ IF ключей нет:                                           │
│             │   - GenerateKeyPair(projectID)                           │
│             │   - SaveToFS(keys/projects/{projectID}_*.pem)            │
│             │   - UpdateProjectPublicKey() → External API              │
│             │                                                          │
│             │ ELSE:                                                    │
│             │   - GetKeyPair(projectID)                                │
│             │   - ValidatePublicKey(task.PublicKey)                    │
│             │                                                          │
│             ▼                                                          │
│  ┌──────────────────┐                                                  │
│  │  AgentService    │                                                  │
│  └────────┬─────────┘                                                  │
│           │                                                            │
│           │ 5. StartAgentForTask(taskID, agentUUID, projectPrivateKey)│
│           ▼                                                            │
│  ┌────────────────────────────────────────────────┐                    │
│  │         Docker Container (Agent)               │                    │
│  │                                                │                    │
│  │  Env:                                          │                    │
│  │  ✅ TASK_ID = "task-123"                        │                    │
│  │  ✅ AGENT_UUID = "uuid-123"                     │                    │
│  │  - API_TOKEN = "orchestrator-token"            │                    │
│  │  ✅ SSH_PRIVATE_KEY (project key, not agent!)   │                    │
│  └──────────────┬─────────────────────────────────┘                    │
│                 │                                                      │
│                 │ 6. POST /api/agent/task                              │
│                 │    Headers: Authorization: Bearer {API_TOKEN}        │
│                 │    Body: {"agent_uuid": "uuid-123"}                  │
│                 │                                                      │
│                 ▼                                                      │
│         ┌───────────────────┐                                          │
│         │  External API     │                                          │
│         │  /api/agent/task  │                                          │
│         └─────────┬─────────┘                                          │
│                   │                                                    │
│                   │ Response:                                          │
│                   │ {                                                  │
│                   │   "id": "task-123",                                │
│                   │   "handler": "CodeGeneratorHandler",               │
│                   │   "handler_options": {                             │
│                   │     "chat": [...],                                 │
│                   │     "prompt": "...",                               │
│                   │     "repo_url": "..."                              │
│                   │   },                                               │
│                   │   "project_id": "project-100",                     │
│                   │   "status": "processing" ← Изменился!              │
│                   │ }                                                  │
│                   │                                                    │
│                   ▼                                                    │
│         ┌──────────────────┐                                           │
│         │  Agent работает  │                                           │
│         │  с задачей       │                                           │
│         └─────────┬────────┘                                           │
│                   │                                                    │
│                   │ 7. PUT /api/agent/task/{taskID}                    │
│                   │    Body: {"completed": true, ...}                  │
│                   │                                                    │
│                   ▼                                                    │
│         ┌────────────────────┐                                         │
│         │  Задача завершена  │                                         │
│         │  status = success  │                                         │
│         └────────────────────┘                                         │
│                                                                        │
└────────────────────────────────────────────────────────────────────────┘

Улучшения:
1. ✅ External API получает agent_uuid при резервировании
2. ✅ Агент знает TASK_ID для получения полных данных
3. ✅ SSH ключи на уровне проекта (общие для всех агентов проекта)
4. ✅ Агент сам вызывает POST /api/agent/task для получения деталей
5. ✅ Двухфазная модель: резервирование → получение задачи
```

## Сравнение SSH ключей

### До изменений (Agent-level keys)

```
keys/
├── agent-uuid-1_private.pem   ← Ключ агента #1
├── agent-uuid-1_public.pub
├── agent-uuid-2_private.pem   ← Ключ агента #2
├── agent-uuid-2_public.pub
└── agent-uuid-3_private.pem   ← Ключ агента #3
    agent-uuid-3_public.pub

Проблема: 
- Каждый агент = уникальный ключ
- Нужно регистрировать каждый ключ в GitHub
- Сложно управлять доступом к репозиторию
```

### После изменений (Project-level keys)

```
keys/
├── agent-uuid-1_private.pem   ← Legacy (не используются для git)
├── agent-uuid-1_public.pub
└── projects/
    ├── project-100_private.pem  ← Ключ проекта #100
    ├── project-100_public.pub
    ├── project-200_private.pem  ← Ключ проекта #200
    └── project-200_public.pub

Преимущества:
- Один ключ на проект (общий для всех агентов)
- Регистрируется один раз в GitHub
- Легко управлять доступом
- Легко ротировать ключи
```

## Двухфазная модель резервирования

### Традиционная модель (PUSH)

```
Агент → POST /api/agent/task → Получает задачу + Резервирует

Плюсы:
✅ Одна операция
✅ Простота

Минусы:
❌ Агент не может "посмотреть" задачу перед резервированием
❌ Невозможно прогнозировать время запуска
❌ Нет гибкости в управлении очередью
```

### Новая модель (PULL with prediction)

```
Оркестратор → GET /orchestrator/tasks/next → Получает информацию
           ↓
        Анализирует задачу:
        - Контекст занят?
        - Достаточно памяти?
        - Сколько времени до запуска?
           ↓
        POST /orchestrator/tasks/{id}/reserve
        Body: {
          reserve_seconds: 300,  ← Прогноз времени!
          agent_uuid: "uuid-123"
        }
           ↓
        Запускает агента
           ↓
        Агент → POST /api/agent/task → Получает полные данные

Плюсы:
✅ Оркестратор анализирует перед резервированием
✅ Прогнозирование времени запуска
✅ Гибкое управление очередью
✅ Можно резервировать несколько задач заранее

Минусы:
❌ Две HTTP операции вместо одной
❌ Сложнее логика
```

## Обработка конфликтов

### Сценарий: 2 оркестратора получают одну задачу

```
Time  Orchestrator A                  Orchestrator B            External API
───────────────────────────────────────────────────────────────────────────────
T0    GET /tasks/next
      ← task-123                                                 task-123 {
                                                                   status: wait,
                                                                   reserved_until: null
                                                                 }

T1                                    GET /tasks/next
                                      ← task-123                 task-123 {
                                                                   status: wait,
                                                                   reserved_until: null
                                                                 }

T2    POST /tasks/123/reserve                                    BEGIN TRANSACTION
      {reserve_seconds: 10,                                      SELECT ... FOR UPDATE
       agent_uuid: "uuid-A"}                                     
                                                                 ✅ Резервирует
                                                                 task-123 {
                                                                   reserved_until: T2+10s,
                                                                   agent_uuid: "uuid-A"
                                                                 }
                                                                 COMMIT
      
      ← 200 OK

T3                                    POST /tasks/123/reserve    BEGIN TRANSACTION
                                      {reserve_seconds: 10,      SELECT ... FOR UPDATE
                                       agent_uuid: "uuid-B"}     
                                                                 ❌ isReserved() = true
                                                                 ROLLBACK
                                      
                                      ← 409 Conflict
                                      {
                                        error: "Task already reserved",
                                        reserved_until: "T2+10s"
                                      }

T4    Запускает агента                ⚠️ Обрабатывает конфликт:
      с uuid-A                        - Логирует warning
                                      - metrics.Conflicts.Inc()
                                      - return nil (не ошибка!)
                                      
T5                                    GET /tasks/next
                                      ← task-456 (следующая задача)
                                      
                                      ✅ Резервирует task-456
```

## Жизненный цикл задачи

```
┌──────────────┐
│  СОЗДАНА     │  status = wait
│              │  agent_id = null
│  task-123    │  agent_uuid = null
│              │  reserved_until = null
└──────┬───────┘
       │
       │ Оркестратор A: GET /tasks/next
       ▼
┌──────────────┐
│  ИНФОРМАЦИЯ  │  status = wait (не изменился!)
│  ПОЛУЧЕНА    │  agent_id = null
│              │  agent_uuid = null
│  task-123    │  reserved_until = null
└──────┬───────┘
       │
       │ Оркестратор A: POST /tasks/123/reserve
       │ {reserve_seconds: 300, agent_uuid: "uuid-A"}
       ▼
┌──────────────┐
│ ЗАРЕЗЕРВИР.  │  status = wait (не изменился!)
│              │  agent_id = 5 (ID оркестратора)
│  task-123    │  agent_uuid = "uuid-A"
│              │  reserved_until = NOW() + 300s
└──────┬───────┘
       │
       │ Оркестратор A: запускает контейнер
       │ Env: TASK_ID=task-123, AGENT_UUID=uuid-A
       ▼
┌──────────────┐
│  КОНТЕЙНЕР   │  status = wait (еще не изменился!)
│  ЗАПУЩЕН     │  agent_id = 5
│              │  agent_uuid = "uuid-A"
│  task-123    │  reserved_until = NOW() + 300s
└──────┬───────┘
       │
       │ Агент внутри контейнера:
       │ POST /api/agent/task
       │ {agent_uuid: "uuid-A"}
       ▼
┌──────────────┐
│  ОБРАБОТКА   │  status = processing ✅ Изменился!
│              │  agent_id = 5
│  task-123    │  agent_uuid = "uuid-A"
│              │  reserved_until = NOW() + 300s (не важно)
└──────┬───────┘
       │
       │ Агент: выполняет задачу
       │
       │ Агент: PUT /api/agent/task/123
       │ {completed: true, ...}
       ▼
┌──────────────┐
│  ЗАВЕРШЕНА   │  status = success ✅
│              │  agent_id = 5
│  task-123    │  agent_uuid = "uuid-A"
│              │  completed_at = NOW()
└──────────────┘
```

## Метрики для мониторинга

### Новые метрики

```go
// Конфликты резервирования
task_reservation_conflicts_total

// Пример использования в Grafana:
rate(task_reservation_conflicts_total[5m]) > 0.1
→ Alert: Высокая частота конфликтов резервирования!
```

### Существующие метрики (без изменений)

```go
tasks_fetched_total       // Всего задач получено
tasks_processed_total     // Всего задач обработано
tasks_failed_total        // Всего задач провалено
active_agents             // Активных агентов
contexts_active           // Активных контекстов
context_queue_length      // Длина очереди контекста
```

## Чек-лист миграции

### Подготовка
- [ ] Создать backup ключей агентов: `cp -r keys keys.backup`
- [ ] Создать feature branch: `git checkout -b feature/orchestrator-api-integration`
- [ ] Настроить mock API для тестирования

### Разработка
- [ ] Создать `internal/ssh/project_keys.go`
- [ ] Обновить `TaskClient.ReserveTask()` - добавить `agent_uuid`
- [ ] Обновить `OrchestratorService.processTask()`
- [ ] Обновить `AgentService.StartAgentForTask()`
- [ ] Добавить метрику `TaskReservationConflictsTotal`

### Тестирование
- [ ] Unit тесты для `ProjectKeyManager`
- [ ] Integration тесты с mock API
- [ ] Тестирование конфликтов резервирования
- [ ] Проверка генерации и валидации SSH ключей

### Деплой
- [ ] Code review
- [ ] Merge в main
- [ ] Создать release tag: `git tag v2.0.0`
- [ ] Deploy на staging
- [ ] Мониторинг метрик
- [ ] Deploy на production

### Проверка
- [ ] Проверить логи: успешное резервирование задач
- [ ] Проверить метрики: `task_reservation_conflicts_total`
- [ ] Проверить SSH ключи: `ls -la keys/projects/`
- [ ] Проверить External API: публичные ключи сохранены

## FAQ

**Q: Что произойдет со старыми агентами?**
A: Ничего! Legacy ключи агентов останутся, но новые агенты будут использовать ключи проектов для git операций.

**Q: Нужно ли обновлять External API?**
A: Нет! External API уже реализован согласно спецификации в docmodule/plane.

**Q: Что если два оркестратора резервируют одну задачу?**
A: Первый получит 200 OK, второй - 409 Conflict. Второй пропустит задачу и возьмет следующую.

**Q: Как часто будут конфликты?**
A: Редко. External API использует `FOR UPDATE SKIP LOCKED`, конфликты возможны только в edge cases.

**Q: Можно ли откатить изменения?**
A: Да! Все изменения обратно совместимы. Старые агенты продолжат работать.

