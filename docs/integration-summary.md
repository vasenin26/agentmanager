# Краткая сводка: Интеграция с Orchestrator API

## Что нужно изменить

### 🔴 Критические изменения

#### 1. Добавить `agent_uuid` в резервирование задач

**Файл:** `internal/external/task_client.go`

```go
// БЫЛО:
func (c *TaskClient) ReserveTask(ctx context.Context, taskID string, reserveSeconds int) error

// СТАЛО:
func (c *TaskClient) ReserveTask(ctx context.Context, taskID string, reserveSeconds int, agentUUID string) error
```

**Payload:**
```go
payload := map[string]interface{}{
    "reserve_seconds": reserveSeconds,
    "agent_uuid":      agentUUID,  // ДОБАВИТЬ
}
```

#### 2. Создать управление SSH ключами проектов

**Новый файл:** `internal/ssh/project_keys.go`

**Основные методы:**
- `GenerateKeyPair(projectID)` - генерация ключей проекта (ОДИН РАЗ для всего проекта)
- `GetKeyPair(projectID)` - получение существующих ключей
- `ValidatePublicKey(projectID, publicKey)` - валидация ключа

**ВАЖНО:** 
- Ключи генерируются **оркестратором** один раз для проекта
- Приватный ключ **передается ВСЕМ агентам** этого проекта
- Публичный ключ **регистрируется** в External API → GitHub/GitLab

**Хранение:**
```
keys/
└── projects/
    ├── {projectId}_private.pem  (права 600) - передается агентам
    └── {projectId}_public.pub   (права 644) - регистрируется в GitHub
```

#### 3. Передать `AGENT_UUID` и `TASK_ID` в контейнер

**Файл:** `internal/service/agent_service.go`

```go
env := []string{
    fmt.Sprintf("TASK_ID=%s", taskID),       // ДОБАВИТЬ
    fmt.Sprintf("AGENT_UUID=%s", agentUUID), // ДОБАВИТЬ
    fmt.Sprintf("API_TOKEN=%s", configOptions.Token),
    // ... остальные
}
```

### 🟡 Важные изменения

#### 4. Обработка конфликтов резервирования (409 Conflict)

```go
if err := os.taskClient.ReserveTask(ctx, task.ID, reserveSeconds, agentUUID); err != nil {
    if strings.Contains(err.Error(), "already reserved") {
        // Пропустить задачу, получим следующую
        metrics.TaskReservationConflictsTotal.Inc()
        return nil
    }
    return fmt.Errorf("failed to reserve task: %w", err)
}
```

#### 5. Метрики для мониторинга

**Файл:** `internal/metrics/metrics.go`

```go
TaskReservationConflictsTotal = prometheus.NewCounter(
    prometheus.CounterOpts{
        Name: "task_reservation_conflicts_total",
        Help: "Total number of task reservation conflicts",
    },
)
```

### 🟢 Вспомогательные изменения

#### 6. Добавить поле `AgentUUID` в `TaskDTO`

```go
type TaskDTO struct {
    ID        string  `json:"id"`
    ContextID *string `json:"context_id"`
    Timeout   int     `json:"timeout"`
    ProjectID string  `json:"project_id"`
    PublicKey *string `json:"public_key"`
    
    AgentUUID string `json:"-"` // Локальное поле (не из API)
}
```

## Workflow после изменений

```
1. GET /orchestrator/tasks/next
   ↓ TaskDTO (id, project_id, context_id, timeout, public_key)

2. Генерация agent_uuid = uuid.New().String()

3. POST /orchestrator/tasks/{id}/reserve
   Body: {"reserve_seconds": 300, "agent_uuid": "..."}
   ↓ Резервирование (reserved_until)

4. Проверка/генерация SSH ключей проекта
   ↓ private_key, public_key

5. Запуск контейнера агента
   Env: TASK_ID, AGENT_UUID, API_TOKEN, SSH_PRIVATE_KEY

6. Агент внутри контейнера:
   POST /api/agent/task 
   Body: {"agent_uuid": "..."}
   ↓ Получение полных данных задачи (handler, handler_options)

7. Агент выполняет задачу

8. Агент: PUT /api/agent/task/{id}
   ↓ Завершение задачи
```

## Ключевые отличия от текущей реализации

| Аспект | Было | Стало |
|--------|------|-------|
| Резервирование | `reserve_seconds` | `reserve_seconds` + `agent_uuid` |
| SSH ключи | Уровень агента (уникальные) | Уровень проекта (общие для всех агентов) |
| Генерация ключей | При запуске каждого агента | Оркестратором один раз для проекта |
| Передача ключей | Уникальный ключ каждому агенту | Один ключ проекта всем агентам |
| Получение задачи агентом | Неясно | `POST /api/agent/task` |
| Конфликты | Не обрабатывались | 409 Conflict → skip task |
| Идентификация воркера | `AGENT_ID` | `AGENT_UUID` (генерируется для каждого запуска) |

## Файлы для изменения

### Новые файлы (создать)
- [ ] `internal/ssh/project_keys.go` - управление ключами проектов
- [ ] `internal/ssh/project_keys_test.go` - тесты
- [ ] `tests/integration/orchestrator_api_test.go` - интеграционные тесты
- [ ] `tests/mock/orchestrator_api.py` - mock API для тестирования
- [ ] `docs/ssh-keys-architecture.md` - документация

### Изменить существующие
- [ ] `internal/external/task_client.go` - добавить `agent_uuid`
- [ ] `internal/service/orchestrator_service.go` - интеграция `ProjectKeyManager`
- [ ] `internal/service/agent_service.go` - передача `AGENT_UUID` и `TASK_ID`
- [ ] `internal/models/task.go` - добавить поле `AgentUUID`
- [ ] `internal/metrics/metrics.go` - добавить метрики конфликтов
- [ ] `docs/task-api-specification.md` - обновить документацию
- [ ] `README.md` - добавить информацию о SSH ключах проектов

## Оценка трудозатрат

| Этап | Время |
|------|-------|
| Разработка | 3-4 дня |
| Тестирование | 1-2 дня |
| Документация | 1 день |
| **Итого** | **5-7 дней** |

## Риски

1. **Конфликты резервирования** - митигация: обработка 409, метрики
2. **Рассинхронизация SSH ключей** - митигация: валидация при каждой задаче
3. **Утечка приватных ключей** - митигация: права 600, логирование

## Быстрый старт

1. **Читать полный план:** `docs/integration-plan.md`
2. **Создать feature branch:** `git checkout -b feature/orchestrator-api-integration`
3. **Реализовать этапы 1-7** последовательно
4. **Тестировать с mock API:** `python tests/mock/orchestrator_api.py`
5. **Code review и деплой**

## Контакты и ссылки

- **Полный план:** [integration-plan.md](./integration-plan.md)
- **Спецификация Task API:** [task-api-specification.md](./task-api-specification.md)
- **Документация External API:** `/docmodule/plane/orchestrator-api-technical-plan.md`
- **FAQ External API:** `/docmodule/docs/ORCHESTRATOR_API_FAQ.md`

