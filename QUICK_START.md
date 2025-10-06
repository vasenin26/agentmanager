# 🚀 Быстрый старт: Интеграция с Orchestrator API

## 📖 Начните здесь!

Вы получили задачу интегрировать AgentManager с Orchestrator API.  
Этот файл - ваша отправная точка.

## ⚡ За 5 минут

### Что нужно сделать?

**3 основных изменения:**

1. **Добавить `agent_uuid`** в резервирование задач
2. **Создать управление SSH ключами на уровне проектов** (генерируются оркестратором)
3. **Передать `TASK_ID` и `AGENT_UUID`** в контейнер агента

### Ключевая концепция SSH ключей

```
❗ ВАЖНО: Ключи генерируются ОРКЕСТРАТОРОМ, не агентами!

Оркестратор:
  ├─ Генерирует SSH ключи ОДИН РАЗ для проекта
  ├─ Публичный ключ → External API → GitHub
  └─ Приватный ключ → передает ВСЕМ агентам проекта

Все агенты проекта используют ОДИНАКОВЫЙ приватный ключ!
```

## 📚 Документация (в порядке чтения)

| # | Файл | Время | Описание |
|---|------|-------|----------|
| 1️⃣ | **[INTEGRATION_CHANGES_SUMMARY.md](INTEGRATION_CHANGES_SUMMARY.md)** | 5 мин | Общая сводка изменений |
| 2️⃣ | **[docs/integration-summary.md](docs/integration-summary.md)** | 15 мин | Краткая техническая сводка |
| 3️⃣ | **[docs/ssh-keys-architecture.md](docs/ssh-keys-architecture.md)** | 30 мин | Архитектура SSH ключей |
| 4️⃣ | **[docs/architecture-comparison.md](docs/architecture-comparison.md)** | 30 мин | Визуальные диаграммы |
| 5️⃣ | **[docs/integration-plan.md](docs/integration-plan.md)** | 2 часа | Полный технический план |

## 🎯 Что изменилось?

### Было → Стало

```diff
// Резервирование задач
- ReserveTask(taskID, reserveSeconds)
+ ReserveTask(taskID, reserveSeconds, agentUUID)

// SSH ключи
- Каждый агент = уникальный ключ (генерируется при запуске агента)
+ Один проект = один ключ (генерируется оркестратором, передается агентам)

// Переменные окружения
  AGENT_ID=...
+ TASK_ID=task-123
+ AGENT_UUID=uuid-456
+ SSH_PRIVATE_KEY=<project-key>  ← Ключ ПРОЕКТА!
```

## 🔧 Файлы для изменения

### Создать (4 новых файла):
- `internal/ssh/project_keys.go` - управление ключами проектов
- `internal/ssh/project_keys_test.go` - тесты
- `tests/integration/orchestrator_api_test.go` - integration тесты
- `tests/mock/orchestrator_api.py` - mock API

### Изменить (5 существующих файлов):
- `internal/external/task_client.go` - добавить `agent_uuid`
- `internal/service/orchestrator_service.go` - генерация ключей проектов
- `internal/service/agent_service.go` - передача переменных
- `internal/models/task.go` - добавить поле `AgentUUID`
- `internal/metrics/metrics.go` - добавить метрику конфликтов

## 🏃 Быстрый workflow

### 1. Изучение (1-2 часа)
```bash
# Прочитать главные документы
cat INTEGRATION_CHANGES_SUMMARY.md
cat docs/integration-summary.md
cat docs/ssh-keys-architecture.md
```

### 2. Создание branch (1 минута)
```bash
git checkout -b feature/orchestrator-api-integration
```

### 3. Реализация (5-7 дней)

**Порядок:**
1. Создать `internal/ssh/project_keys.go` (1 час)
2. Обновить `TaskClient.ReserveTask()` (30 мин)
3. Интегрировать в `OrchestratorService` (1 час)
4. Передать переменные в контейнер (30 мин)
5. Добавить метрики (15 мин)
6. Тестирование (2-3 часа)

### 4. Тестирование
```bash
# Unit тесты
go test ./internal/ssh/... -v
go test ./internal/external/... -v

# Mock API
python tests/mock/orchestrator_api.py &
TASK_API_URL=http://localhost:9000/api/v1/orchestrator ./bin/agentmanager
```

### 5. Коммит и PR
```bash
git add .
git commit -m "feat: integrate with Orchestrator API"
git push origin feature/orchestrator-api-integration
```

## 💡 Ключевые моменты

### SSH ключи генерируются оркестратором!

```go
// ❌ НЕ ТАК (агент НЕ генерирует ключи!)
func Agent.Start() {
    sshKey := generateSSHKey() // ❌ Неправильно!
}

// ✅ ТАК (оркестратор генерирует и передает ключи)
func Orchestrator.processTask(task) {
    // Генерируем ключи ОДИН РАЗ для проекта
    projectKey := projectKeyManager.GenerateKeyPair(task.ProjectID)
    
    // Передаем приватный ключ ВСЕМ агентам проекта
    agent.Start(
        taskID,
        agentUUID,
        projectKey.PrivateKey, // ← Ключ ПРОЕКТА!
    )
}
```

### Один ключ для всех агентов проекта

```
Project #100:
  ├── SSH Key: project-100_private.pem (generated ONCE)
  │
  └── Agents:
      ├── Agent #1 → SSH_PRIVATE_KEY=<same>
      ├── Agent #2 → SSH_PRIVATE_KEY=<same>
      └── Agent #3 → SSH_PRIVATE_KEY=<same>
```

## 🐛 Troubleshooting

### Ошибка: Task reservation failed (400)
```bash
# Проверить payload - должен содержать agent_uuid!
curl -X POST $TASK_API_URL/tasks/task-1/reserve \
  -d '{"reserve_seconds": 10, "agent_uuid": "uuid-123"}'
```

### Ошибка: SSH keys generation failed
```bash
# Проверить права на директорию
mkdir -p keys/projects
chmod 700 keys/projects
```

### Агент не получает задачу
```bash
# Проверить переменные окружения
docker exec <container> env | grep -E "TASK_ID|AGENT_UUID"
```

## 📞 Помощь

### Нашли проблему?
1. Проверьте [docs/integration-plan.md](docs/integration-plan.md) - раздел Troubleshooting
2. Посмотрите примеры в [docs/architecture-comparison.md](docs/architecture-comparison.md)
3. Изучите [docs/ssh-keys-architecture.md](docs/ssh-keys-architecture.md) - FAQ

### Нужны детали?
- **SSH ключи:** [docs/ssh-keys-architecture.md](docs/ssh-keys-architecture.md)
- **Визуальные диаграммы:** [docs/architecture-comparison.md](docs/architecture-comparison.md)
- **Полный план:** [docs/integration-plan.md](docs/integration-plan.md)

## ✅ Checklist

Перед началом работы убедитесь:

- [ ] Прочитали `INTEGRATION_CHANGES_SUMMARY.md`
- [ ] Прочитали `docs/integration-summary.md`
- [ ] Поняли концепцию SSH ключей на уровне проектов
- [ ] Понимаете что ключи генерируются **оркестратором**, не агентами
- [ ] Создали feature branch
- [ ] Готовы к реализации

---

**🎉 Успехов в интеграции!**

Начните с [INTEGRATION_CHANGES_SUMMARY.md](INTEGRATION_CHANGES_SUMMARY.md) →

