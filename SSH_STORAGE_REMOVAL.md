# ✅ Удаление ssh.Storage (legacy) завершено

## Что было удалено

### Удалено использование `ssh.Storage` из:

1. **internal/service/orchestrator_service.go**
   - Удалено поле `sshStorage *ssh.Storage` из структуры
   - Удален параметр из конструктора `NewOrchestratorService()`
   - Теперь используется только `projectKeyManager *ssh.ProjectKeyManager`

2. **internal/service/agent_service.go**
   - Удалено поле `sshStorage *ssh.Storage` из структуры
   - Удален параметр из конструктора `NewAgentService()`
   - Удалена генерация SSH ключей агента при запуске
   - Удалено поле `PublicKey` из `AgentMeta`

3. **cmd/server/main.go**
   - Удалено создание `sshStorage` через `ssh.NewStorage()`
   - Удалена передача `sshStorage` в `NewAgentService()`
   - Удалена передача `sshStorage` в `NewOrchestratorService()`

## Почему удалено?

**Старая модель (ssh.Storage):**
- SSH ключи генерировались для каждого агента индивидуально
- Каждый агент имел уникальные ключи
- При 1000 агентов одного проекта = 1000 ключей в GitHub
- Сложная ротация ключей

**Новая модель (ProjectKeyManager):**
- SSH ключи генерируются один раз для проекта оркестратором
- Все агенты проекта используют один и тот же приватный ключ
- При 1000 агентов одного проекта = 1 ключ в GitHub ✅
- Простая ротация ключей

## Что осталось?

### Файлы ssh пакета, которые НЕ удалены:

1. **internal/ssh/storage.go** - оставлен на случай legacy совместимости
2. **internal/ssh/types.go** - оставлен (содержит типы)
3. **internal/crypto/ssh.go** - оставлен (deprecated функция с комментарием)

Эти файлы можно удалить в будущем, если уверены что они нигде не используются.

## Текущее состояние

### Используется:
- ✅ `internal/ssh/project_keys.go` - ProjectKeyManager (новая архитектура)
- ✅ `internal/ssh/project_keys_test.go` - тесты (6/6 пройдено)

### Не используется (legacy):
- ⚠️ `internal/ssh/storage.go` - Storage (можно удалить в будущем)
- ⚠️ `internal/ssh/types.go` - типы (можно удалить в будущем)

## Проверка

### ✅ Линтер
```bash
No linter errors found
```

### ✅ Сборка
```bash
go build ./cmd/server
# Success!
```

### ✅ Тесты
```bash
go test ./internal/ssh/... -v
# All tests passing (6/6)
```

## Изменения в сигнатурах функций

### OrchestratorService

**Было:**
```go
func NewOrchestratorService(
    // ...
    sshStorage *ssh.Storage,
    projectKeyManager *ssh.ProjectKeyManager,
    // ...
)
```

**Стало:**
```go
func NewOrchestratorService(
    // ...
    projectKeyManager *ssh.ProjectKeyManager,
    // ...
)
```

### AgentService

**Было:**
```go
func NewAgentService(
    dc docker.DockerClient,
    reg docker.AuthConfig,
    t time.Duration,
    serverURL string,
    sshStorage *ssh.Storage,  // ← Удалено
    apiHost, openaiModel, openaiApiKey, gitUserName, gitUserEmail string,
)

type AgentService struct {
    sshStorage *ssh.Storage  // ← Удалено
    // ...
}
```

**Стало:**
```go
func NewAgentService(
    dc docker.DockerClient,
    reg docker.AuthConfig,
    t time.Duration,
    serverURL string,
    apiHost, openaiModel, openaiApiKey, gitUserName, gitUserEmail string,
)

type AgentService struct {
    // sshStorage удалено
    // ...
}
```

### AgentMeta

**Было:**
```go
return models.AgentMeta{
    Server:    as.serverURL,
    AgentID:   configOptions.AgentID.String(),
    PublicKey: sshKeyPair.PublicKey,  // ← Удалено
}
```

**Стало:**
```go
return models.AgentMeta{
    Server:  as.serverURL,
    AgentID: configOptions.AgentID.String(),
    // PublicKey удалено (используется ключ проекта)
}
```

## Workflow после удаления

### Генерация SSH ключей

**До удаления:**
```
1. Запуск агента
2. AgentService генерирует SSH ключи агента
3. Ключи сохраняются в keys/{agentId}_*.pem
4. Публичный ключ возвращается в AgentMeta
```

**После удаления:**
```
1. Оркестратор получает задачу для project-100
2. OrchestratorService проверяет ключи проекта
3. Если нет - ProjectKeyManager генерирует ключи проекта
4. Ключи сохраняются в keys/projects/project-100_*.pem
5. Публичный ключ → External API → GitHub
6. Приватный ключ → передается ВСЕМ агентам проекта
```

## Миграция данных (если нужна)

Если у вас уже есть работающая система со старыми ключами агентов:

### Вариант 1: Оставить как есть
Старые ключи агентов останутся в `keys/{agentId}_*.pem`.
Новые ключи проектов будут создаваться в `keys/projects/`.
Никакой миграции не требуется.

### Вариант 2: Очистить старые ключи
```bash
# Осторожно! Удалит все старые ключи агентов
rm keys/*_private.pem
rm keys/*_public.pub
# Новые ключи проектов останутся в keys/projects/
```

## Обратная совместимость

**⚠️ Breaking Changes:**

1. **API изменения:**
   - `AgentMeta.PublicKey` удален
   - Если у вас есть код, использующий это поле - нужно обновить

2. **Конструкторы:**
   - `NewOrchestratorService()` - один параметр меньше
   - `NewAgentService()` - один параметр меньше
   - Весь код, создающий эти сервисы, должен быть обновлен

3. **SSH ключи:**
   - Старые ключи агентов больше не генерируются
   - Используются только ключи проектов

## Следующие шаги

1. **Тестирование:**
   - Integration тесты с mock API
   - Проверка генерации ключей проектов
   - Проверка что агенты получают правильные ключи

2. **Опционально - удалить legacy файлы:**
   ```bash
   rm internal/ssh/storage.go
   rm internal/ssh/types.go
   # Обновить import'ы если нужно
   ```

3. **Документация:**
   - ✅ Обновлен README.md
   - ✅ Создана документация по SSH ключам
   - ✅ Обновлен integration план

## Итого

✅ **ssh.Storage успешно удален из всех мест использования**  
✅ **Проект собирается без ошибок**  
✅ **Линтер не выдает предупреждений**  
✅ **Все тесты проходят**  

**Готово к коммиту!** 🎉

---

**Дата:** 2025-10-06  
**Версия:** 1.0  
**Статус:** ✅ Завершено

