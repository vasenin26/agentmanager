# План интеграции AgentManager с Orchestrator API

## Обзор

Данный план описывает необходимые изменения в AgentManager (Go) для работы с внешним Orchestrator API (реализован в docmodule/plane на Laravel/PHP).

## Текущее состояние

### AgentManager (Go оркестратор)
- ✅ Реализован pull-based механизм получения задач
- ✅ Есть `TaskClient` для работы с внешним API
- ✅ Поддержка контекстов (Docker volumes)
- ✅ Управление SSH ключами для **агентов**
- ✅ Двухфазное резервирование (fetch → reserve)
- ❌ **НЕТ поддержки** `agent_uuid` в запросах резервирования
- ❌ **НЕТ управления** SSH ключами для **проектов**

### External API (docmodule/plane)
- ✅ Реализован Orchestrator API (`/api/v1/orchestrator`)
- ✅ Поддержка резервирования задач с `agent_uuid`
- ✅ Управление SSH ключами на уровне **проектов**
- ✅ Поле `reserved_until` и автоматическое освобождение задач
- ✅ Защита от race conditions через `FOR UPDATE SKIP LOCKED`

## Ключевые различия и проблемы

### 1. Модель SSH ключей

| Аспект | AgentManager (текущая) | External API (требуемая) |
|--------|------------------------|--------------------------|
| Scope | **Агент** (индивидуальные) | **Проект** (общие для всех агентов) |
| Генерация | При запуске каждого агента | Оркестратором один раз для проекта |
| Хранение | Файловая система оркестратора | Публичный ключ в БД (projects.public_key) |
| Передача агенту | В контейнер каждого агента | **Приватный ключ проекта** в контейнер |
| Регистрация | Не регистрируются | Публичный ключ → External API → GitHub/GitLab |

**Проблема:** AgentManager генерирует уникальные ключи для каждого агента, а External API ожидает **общие ключи на уровне проекта**, которые используются всеми агентами этого проекта.

**Решение:** 
- Оркестратор **генерирует SSH ключи один раз для проекта** (не для каждого агента)
- Оркестратор **передает приватный ключ проекта** всем агентам этого проекта
- Публичный ключ проекта отправляется в External API для регистрации в GitHub/GitLab
- Все агенты проекта используют **один и тот же SSH ключ проекта** для git-операций

### 2. Поле `agent_uuid` в резервировании

| Сторона | Текущая реализация | Требуемая |
|---------|-------------------|-----------|
| AgentManager | Отсутствует в `ReserveTask()` | Должна отправлять `agent_uuid` |
| External API | N/A | Ожидает `reserve_seconds` + `agent_uuid` |

**Проблема:** External API требует `agent_uuid` при резервировании для идентификации воркера.

**Решение:** Расширить метод `ReserveTask()` и добавить поле в запрос.

### 3. Получение детальной информации о задаче

| Аспект | AgentManager | External API |
|--------|--------------|--------------|
| `/tasks/next` | Возвращает базовую информацию | Только базовая информация |
| Детали задачи | Неясно откуда брать | Агент получает через `POST /api/agent/task` |

**Проблема:** `OrchestratorTaskDTO` возвращает только `id`, `context_id`, `timeout`, `project_id`, `public_key`. Откуда AgentManager получит детали задачи (handler, handler_options и т.д.)?

**Решение:** 
- AgentManager передает `task_id` и `agent_uuid` в контейнер агента
- Агент сам вызывает `POST /api/agent/task` для получения полных данных
- AgentManager не нуждается в деталях задачи для её запуска

### 4. Жизненный цикл задачи

**External API workflow:**
```
1. GET /orchestrator/tasks/next → TaskDTO (status=wait)
2. POST /orchestrator/tasks/{id}/reserve → резервирование (status=wait, reserved_until)
3. Агент: POST /api/agent/task → получение задачи (status=processing)
4. Агент: PUT /api/agent/task/{id} → обновление/завершение
```

**AgentManager workflow (текущий):**
```
1. FetchTask() → получение TaskDTO
2. ReserveTask() → резервирование
3. StartAgentForTask() → запуск контейнера
4. Агент внутри контейнера работает с задачей
```

**Проблема:** Агент должен сам вызвать `POST /api/agent/task` после запуска, но AgentManager не передает необходимый контекст.

**Решение:**
- AgentManager передает в контейнер: `TASK_ID`, `AGENT_UUID`, `API_TOKEN`
- Агент в контейнере вызывает `POST /api/agent/task` с этими данными
- Агент получает полную информацию о задаче и выполняет её

## План работ

### Этап 1: Расширение модели TaskDTO

**Цель:** Убедиться, что все поля из External API корректно маппятся.

#### 1.1 Обновить `internal/models/task.go`

**Файл:** `internal/models/task.go`

**Текущее состояние:**
```go
type TaskDTO struct {
	ID        string  `json:"id"`
	ContextID *string `json:"context_id"`
	Timeout   int     `json:"timeout"`
	ProjectID string  `json:"project_id"`
	PublicKey *string `json:"public_key"`
}
```

**Требуемое состояние:**
```go
type TaskDTO struct {
	ID        string  `json:"id"`         // ID задачи
	ContextID *string `json:"context_id"` // ID контекста (nil если не требуется)
	Timeout   int     `json:"timeout"`    // Таймаут выполнения в секундах
	ProjectID string  `json:"project_id"` // ID проекта
	PublicKey *string `json:"public_key"` // SSH публичный ключ проекта (может быть nil)
}
```

**Изменения:** Нет (структура уже соответствует спецификации External API).

**Статус:** ✅ Соответствует

---

### Этап 2: Добавление поддержки `agent_uuid` в резервирование

**Цель:** External API требует `agent_uuid` при резервировании задачи.

#### 2.1 Обновить метод `ReserveTask()` в `TaskClient`

**Файл:** `internal/external/task_client.go`

**Текущая сигнатура:**
```go
func (c *TaskClient) ReserveTask(ctx context.Context, taskID string, reserveSeconds int) error
```

**Требуемая сигнатура:**
```go
func (c *TaskClient) ReserveTask(ctx context.Context, taskID string, reserveSeconds int, agentUUID string) error
```

**Изменения в теле метода:**

**Было:**
```go
payload := map[string]int{
	"reserve_seconds": reserveSeconds,
}
```

**Станет:**
```go
payload := map[string]interface{}{
	"reserve_seconds": reserveSeconds,
	"agent_uuid":      agentUUID,
}
```

**Обработка ответа (добавить):**
```go
// Обработать 409 Conflict
if resp.StatusCode == http.StatusConflict {
	var conflictResp struct {
		Error        string `json:"error"`
		ReservedBy   string `json:"reserved_by,omitempty"`
		ReservedUntil string `json:"reserved_until,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&conflictResp); err == nil {
		c.logger.Warn("Task reservation conflict",
			zap.String("taskID", taskID),
			zap.String("error", conflictResp.Error),
			zap.String("reserved_until", conflictResp.ReservedUntil))
	}
	return fmt.Errorf("task already reserved: %s", conflictResp.Error)
}
```

#### 2.2 Обновить вызовы `ReserveTask()` в `OrchestratorService`

**Файл:** `internal/service/orchestrator_service.go`

**Метод:** `processTask()`

**Было:**
```go
if err := os.taskClient.ReserveTask(ctx, task.ID, reserveSeconds); err != nil {
	// ...
}
```

**Станет:**
```go
// Генерировать agent_uuid для воркера
agentUUID := uuid.New().String()

// Зарезервировать задачу с указанием воркера
if err := os.taskClient.ReserveTask(ctx, task.ID, reserveSeconds, agentUUID); err != nil {
	os.logger.Error("Failed to reserve task", 
		zap.String("taskID", task.ID), 
		zap.String("agentUUID", agentUUID),
		zap.Error(err))
	return fmt.Errorf("failed to reserve task: %w", err)
}

// Сохранить agentUUID для передачи в контейнер
task.AgentUUID = agentUUID
```

**Изменения в `TaskDTO`:**
Добавить поле для временного хранения `agentUUID`:

```go
type TaskDTO struct {
	ID        string  `json:"id"`
	ContextID *string `json:"context_id"`
	Timeout   int     `json:"timeout"`
	ProjectID string  `json:"project_id"`
	PublicKey *string `json:"public_key"`
	
	// Локальное поле (не из API)
	AgentUUID string `json:"-"` // UUID воркера (генерируется локально)
}
```

---

### Этап 3: Управление SSH ключами проектов

**Цель:** Реализовать механизм управления SSH ключами на уровне проектов (а не агентов).

#### 3.1 Создать новый модуль `internal/ssh/project_keys.go`

**Структура:**

```go
package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	
	"golang.org/x/crypto/ssh"
)

// ProjectKeyManager управляет SSH ключами на уровне проектов
type ProjectKeyManager struct {
	keysDir string // Директория для хранения ключей проектов
}

// NewProjectKeyManager создает новый менеджер ключей проектов
func NewProjectKeyManager(keysDir string) (*ProjectKeyManager, error) {
	// Создать директорию для ключей проектов
	projectKeysDir := filepath.Join(keysDir, "projects")
	if err := os.MkdirAll(projectKeysDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create projects keys directory: %w", err)
	}
	
	return &ProjectKeyManager{
		keysDir: projectKeysDir,
	}, nil
}

// GenerateKeyPair генерирует пару SSH ключей для проекта
func (m *ProjectKeyManager) GenerateKeyPair(projectID string) (privateKey, publicKey string, err error) {
	// Генерировать RSA ключ
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate RSA key: %w", err)
	}
	
	// Приватный ключ в PEM формате
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	
	// Публичный ключ в OpenSSH формате
	publicSSHKey, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate public key: %w", err)
	}
	publicKeySSH := ssh.MarshalAuthorizedKey(publicSSHKey)
	
	// Сохранить ключи в файловую систему
	if err := m.saveKeys(projectID, privateKeyPEM, publicKeySSH); err != nil {
		return "", "", err
	}
	
	return string(privateKeyPEM), string(publicKeySSH), nil
}

// GetKeyPair получает существующую пару ключей проекта
func (m *ProjectKeyManager) GetKeyPair(projectID string) (privateKey, publicKey string, err error) {
	privateKeyPath := filepath.Join(m.keysDir, fmt.Sprintf("%s_private.pem", projectID))
	publicKeyPath := filepath.Join(m.keysDir, fmt.Sprintf("%s_public.pub", projectID))
	
	// Проверить существование ключей
	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		return "", "", fmt.Errorf("project keys not found")
	}
	
	// Читать приватный ключ
	privateKeyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read private key: %w", err)
	}
	
	// Читать публичный ключ
	publicKeyBytes, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read public key: %w", err)
	}
	
	return string(privateKeyBytes), string(publicKeyBytes), nil
}

// KeyPairExists проверяет существование ключей проекта
func (m *ProjectKeyManager) KeyPairExists(projectID string) bool {
	privateKeyPath := filepath.Join(m.keysDir, fmt.Sprintf("%s_private.pem", projectID))
	_, err := os.Stat(privateKeyPath)
	return err == nil
}

// ValidatePublicKey проверяет соответствие публичного ключа
func (m *ProjectKeyManager) ValidatePublicKey(projectID, publicKeyToValidate string) (bool, error) {
	_, existingPublicKey, err := m.GetKeyPair(projectID)
	if err != nil {
		return false, err
	}
	
	return existingPublicKey == publicKeyToValidate, nil
}

// saveKeys сохраняет ключи в файловую систему
func (m *ProjectKeyManager) saveKeys(projectID string, privateKey, publicKey []byte) error {
	privateKeyPath := filepath.Join(m.keysDir, fmt.Sprintf("%s_private.pem", projectID))
	publicKeyPath := filepath.Join(m.keysDir, fmt.Sprintf("%s_public.pub", projectID))
	
	// Сохранить приватный ключ (права 600)
	if err := os.WriteFile(privateKeyPath, privateKey, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}
	
	// Сохранить публичный ключ (права 644)
	if err := os.WriteFile(publicKeyPath, publicKey, 0644); err != nil {
		return fmt.Errorf("failed to write public key: %w", err)
	}
	
	return nil
}
```

#### 3.2 Интегрировать `ProjectKeyManager` в `OrchestratorService`

**Файл:** `internal/service/orchestrator_service.go`

**Добавить поле:**
```go
type OrchestratorService struct {
	// ... существующие поля
	projectKeyManager *ssh.ProjectKeyManager // Добавить
}
```

**Обновить конструктор:**
```go
func NewOrchestratorService(
	// ... существующие параметры
	projectKeyManager *ssh.ProjectKeyManager, // Добавить
) *OrchestratorService {
	return &OrchestratorService{
		// ... существующие поля
		projectKeyManager: projectKeyManager, // Добавить
	}
}
```

#### 3.3 Обновить метод `validateAndPrepareProjectKey()`

**Заменить использование `sshStorage` на `projectKeyManager`:**

```go
// validateAndPrepareProjectKey проверяет и подготавливает SSH ключи для проекта
// Ключи генерируются ОДИН РАЗ для проекта и используются ВСЕМИ агентами этого проекта
func (os *OrchestratorService) validateAndPrepareProjectKey(ctx context.Context, task *models.TaskDTO) (privateKey, publicKey string, err error) {
	projectID := task.ProjectID
	
	// Проверяем существование ключей для проекта
	if os.projectKeyManager.KeyPairExists(projectID) {
		// Ключи проекта уже существуют - используем их
		privateKey, publicKey, err := os.projectKeyManager.GetKeyPair(projectID)
		if err != nil {
			return "", "", fmt.Errorf("failed to get project key pair: %w", err)
		}
		
		os.logger.Info("Using existing project SSH keys",
			zap.String("projectID", projectID))
		
		// Если публичный ключ передан в задаче, проверяем его
		if task.PublicKey != nil && *task.PublicKey != "" {
			isValid, err := os.projectKeyManager.ValidatePublicKey(projectID, *task.PublicKey)
			if err != nil {
				os.logger.Error("Failed to validate public key",
					zap.String("projectID", projectID),
					zap.Error(err))
				return "", "", fmt.Errorf("failed to validate public key: %w", err)
			}
			
			if !isValid {
				os.logger.Warn("Public key mismatch, generating new key pair",
					zap.String("projectID", projectID))
				// Публичный ключ не совпадает - генерируем новую пару
				return os.generateAndRegisterProjectKey(ctx, projectID)
			}
		}
		
		// Ключи валидны - возвращаем их для передачи агенту
		return privateKey, publicKey, nil
	}
	
	// Ключей нет - генерируем новую пару для проекта
	os.logger.Info("Project keys not found, generating new SSH key pair for project",
		zap.String("projectID", projectID))
	return os.generateAndRegisterProjectKey(ctx, projectID)
}

// generateAndRegisterProjectKey генерирует новую пару SSH ключей для проекта
// и регистрирует публичный ключ в External API
// ВАЖНО: Ключи генерируются ОДИН РАЗ для всего проекта, не для каждого агента!
func (os *OrchestratorService) generateAndRegisterProjectKey(ctx context.Context, projectID string) (privateKey, publicKey string, err error) {
	os.logger.Info("Generating NEW SSH key pair for project (will be shared by all agents)",
		zap.String("projectID", projectID))
	
	// Генерируем новую пару ключей для проекта
	privateKey, publicKey, err = os.projectKeyManager.GenerateKeyPair(projectID)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate project key pair: %w", err)
	}
	
	// Отправляем публичный ключ в External API
	// External API сохранит его в БД и зарегистрирует в GitHub/GitLab
	if err := os.taskClient.UpdateProjectPublicKey(ctx, projectID, publicKey); err != nil {
		os.logger.Error("Failed to update project public key in API",
			zap.String("projectID", projectID),
			zap.Error(err))
		// Не критично - ключи уже сохранены локально, продолжаем
	} else {
		os.logger.Info("Successfully registered project public key in External API",
			zap.String("projectID", projectID),
			zap.String("publicKeyPrefix", publicKey[:50]+"..."))
	}
	
	// Возвращаем приватный ключ для передачи агенту
	return privateKey, publicKey, nil
}
```

#### 3.4 Обновить передачу SSH ключа в контейнер

**Файл:** `internal/service/orchestrator_service.go`

**Метод:** `startAgentForTask()`

**ВАЖНО:** Приватный ключ **проекта** (не агента!) передается в контейнер.
Все агенты одного проекта получают **один и тот же** приватный ключ проекта.

**Текущий код:**
```go
// Запустить агента с приватным ключом проекта
agentMeta, err := os.agentService.StartAgentForTask(
	configOptions,
	task.ID,
	contextVolumeID,
	memoryLimit,
	projectKeys.PrivateKey, // Передаем приватный ключ проекта
)
```

**Обновленный код:**
```go
// Получить SSH ключи проекта (генерируются один раз для всего проекта)
// Все агенты этого проекта будут использовать ОДИНАКОВЫЕ ключи
privateKey, _, err := os.validateAndPrepareProjectKey(ctx, task)
if err != nil {
	return fmt.Errorf("failed to validate project keys: %w", err)
}

os.logger.Info("Passing project SSH private key to agent",
	zap.String("projectID", task.ProjectID),
	zap.String("agentUUID", task.AgentUUID))

// Запустить агента с приватным ключом проекта
// ВАЖНО: privateKey - это ключ ПРОЕКТА, общий для всех агентов проекта
agentMeta, err := os.agentService.StartAgentForTask(
	configOptions,
	task.ID,
	task.AgentUUID, // Передать agent_uuid
	contextVolumeID,
	memoryLimit,
	privateKey, // Приватный ключ ПРОЕКТА (не агента!)
)
```

---

### Этап 4: Передача `agent_uuid` и `task_id` в контейнер агента

**Цель:** Агент должен получить `TASK_ID` и `AGENT_UUID` для вызова `POST /api/agent/task`.

#### 4.1 Обновить `AgentService.StartAgentForTask()`

**Файл:** `internal/service/agent_service.go`

**Обновить сигнатуру:**

**Было:**
```go
func (s *AgentService) StartAgentForTask(
	configOptions models.ConfigOptions,
	taskID string,
	contextVolumeID *string,
	memoryLimit int64,
	sshPrivateKey string,
) (*models.AgentMeta, error)
```

**Станет:**
```go
func (s *AgentService) StartAgentForTask(
	configOptions models.ConfigOptions,
	taskID string,
	agentUUID string, // Добавить
	contextVolumeID *string,
	memoryLimit int64,
	sshPrivateKey string,
) (*models.AgentMeta, error)
```

**Добавить переменную окружения в контейнер:**

```go
env := []string{
	fmt.Sprintf("AGENT_ID=%s", configOptions.AgentID.String()),
	fmt.Sprintf("AGENT_UUID=%s", agentUUID), // Добавить - UUID воркера для резервирования
	fmt.Sprintf("TASK_ID=%s", taskID),       // Добавить - ID задачи для получения деталей
	fmt.Sprintf("API_TOKEN=%s", configOptions.Token),
	fmt.Sprintf("SSH_PRIVATE_KEY=%s", sshPrivateKey), // ВАЖНО: это ключ ПРОЕКТА, не агента!
	// ... остальные переменные
}
```

**Комментарий:** `SSH_PRIVATE_KEY` теперь содержит приватный ключ **проекта**, который:
- Генерируется **один раз** для проекта оркестратором
- Передается **всем агентам** этого проекта
- Используется агентами для git-операций (clone, push, pull)

#### 4.2 Обновить вызов в `OrchestratorService`

**Файл:** `internal/service/orchestrator_service.go`

**Метод:** `startAgentForTask()`

**Обновить вызов:**
```go
agentMeta, err := os.agentService.StartAgentForTask(
	configOptions,
	task.ID,
	task.AgentUUID, // Передать agent_uuid
	contextVolumeID,
	memoryLimit,
	privateKey,
)
```

---

### Этап 5: Обработка конфликтов резервирования

**Цель:** Корректно обрабатывать ситуации, когда задача уже зарезервирована другим оркестратором.

#### 5.1 Добавить логику retry в `processTask()`

**Файл:** `internal/service/orchestrator_service.go`

**Обновить метод `processTask()`:**

```go
func (os *OrchestratorService) processTask(ctx context.Context, task *models.TaskDTO) error {
	os.logger.Info("Processing task", zap.String("taskID", task.ID))
	
	// Прогнозировать время до запуска агента
	reserveSeconds := os.estimateTimeUntilStart(ctx, task)
	os.logger.Info("Estimated time until agent start",
		zap.String("taskID", task.ID),
		zap.Int("reserveSeconds", reserveSeconds))
	
	// Генерировать agent_uuid для воркера
	agentUUID := uuid.New().String()
	
	// Зарезервировать задачу с указанием воркера
	if err := os.taskClient.ReserveTask(ctx, task.ID, reserveSeconds, agentUUID); err != nil {
		// Проверить, не конфликт ли резервирования
		if strings.Contains(err.Error(), "already reserved") {
			os.logger.Warn("Task reservation conflict, skipping task",
				zap.String("taskID", task.ID),
				zap.String("agentUUID", agentUUID))
			// Не критично - пропускаем задачу, получим следующую
			return nil
		}
		
		os.logger.Error("Failed to reserve task",
			zap.String("taskID", task.ID),
			zap.String("agentUUID", agentUUID),
			zap.Error(err))
		return fmt.Errorf("failed to reserve task: %w", err)
	}
	
	// Сохранить agentUUID для передачи в контейнер
	task.AgentUUID = agentUUID
	
	// ... остальная логика без изменений
}
```

#### 5.2 Добавить метрику конфликтов

**Файл:** `internal/metrics/metrics.go`

**Добавить метрику:**
```go
var (
	// ... существующие метрики
	
	TaskReservationConflictsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "task_reservation_conflicts_total",
			Help: "Total number of task reservation conflicts",
		},
	)
)

func init() {
	// ... регистрация других метрик
	prometheus.MustRegister(TaskReservationConflictsTotal)
}
```

**Использовать в `processTask()`:**
```go
if strings.Contains(err.Error(), "already reserved") {
	metrics.TaskReservationConflictsTotal.Inc() // Добавить
	// ...
}
```

---

### Этап 6: Обновление документации

**Цель:** Документировать все изменения и новый workflow.

#### 6.1 Обновить `docs/task-api-specification.md`

**Добавить раздел:**

```markdown
## Важные изменения в резервировании

### Поле `agent_uuid`

С версии X.X.X поле `agent_uuid` является **обязательным** при резервировании задачи:

```json
{
  "reserve_seconds": 300,
  "agent_uuid": "550e8400-e29b-41d4-a716-446655440000"
}
```

### Workflow с External API

1. **Оркестратор:** GET /api/v1/orchestrator/tasks/next
   - Получает базовую информацию о задаче
   
2. **Оркестратор:** POST /api/v1/orchestrator/tasks/{id}/reserve
   - Резервирует задачу с `agent_uuid` и `reserve_seconds`
   
3. **Оркестратор:** Запускает контейнер агента
   - Передает переменные: `TASK_ID`, `AGENT_UUID`, `API_TOKEN`
   
4. **Агент:** POST /api/agent/task
   - Получает полную информацию о задаче
   - Переводит задачу в статус `processing`
   
5. **Агент:** Выполняет задачу
   
6. **Агент:** PUT /api/agent/task/{id}
   - Обновляет задачу (завершение)
```

#### 6.2 Создать `docs/ssh-keys-architecture.md`

**Новый файл** с описанием архитектуры управления SSH ключами:

```markdown
# Архитектура управления SSH ключами

## Двухуровневая модель

### Уровень 1: SSH ключи агентов (Legacy)

**Scope:** Каждый агент (контейнер)
**Назначение:** Идентификация агента, коммуникация с оркестратором
**Хранение:** `{SSH_KEYS_DIR}/{agentId}_private.pem`
**Жизненный цикл:** Генерируются при первом запуске агента, переиспользуются

### Уровень 2: SSH ключи проектов (New)

**Scope:** Проект (набор задач, репозиторий)
**Назначение:** Git операции (clone, push, pull)
**Хранение:** `{SSH_KEYS_DIR}/projects/{projectId}_private.pem`
**Жизненный цикл:** 
- Генерируются оркестратором при получении первой задачи проекта
- Публичный ключ отправляется в External API
- External API добавляет ключ в GitHub/GitLab
- Переиспользуются всеми агентами проекта

## Workflow генерации ключей проекта

```
1. Оркестратор получает задачу для project_id=123
2. Проверяет наличие ключей: ProjectKeyManager.KeyPairExists("123")
3. Если нет:
   a. Генерирует пару: ProjectKeyManager.GenerateKeyPair("123")
   b. Сохраняет локально: /keys/projects/123_private.pem
   c. Отправляет public_key: PUT /api/v1/orchestrator/projects/123/key
4. Если есть:
   a. Валидирует public_key из API
   b. Если не совпадает - генерирует новые
5. Передает private_key в контейнер агента
```

## Безопасность

- Приватные ключи агентов: **права 600**, уникальны для каждого агента
- Приватные ключи проектов: **права 600**, общие для всех агентов проекта
- Публичные ключи проектов: **права 644**, отправляются в External API
- Директория ключей: **права 700**
```

#### 6.3 Обновить `README.md`

**Добавить раздел о SSH ключах проектов:**

```markdown
## SSH Ключи

### Двухуровневая модель

Agent Service использует двухуровневую модель SSH ключей:

1. **Ключи агентов** - для идентификации агента (legacy)
2. **Ключи проектов** - для git операций (new)

Подробнее: [SSH Keys Architecture](docs/ssh-keys-architecture.md)

### Переменные окружения

| Переменная | Описание | Пример |
|------------|----------|--------|
| `SSH_KEYS_DIR` | Директория для хранения SSH ключей | `./keys` |

### Структура хранения

```
keys/
├── {agentId}_private.pem     # Ключи агентов (legacy)
├── {agentId}_public.pub
└── projects/
    ├── {projectId}_private.pem  # Ключи проектов (new)
    └── {projectId}_public.pub
```
```

---

### Этап 7: Тестирование

**Цель:** Убедиться, что все изменения работают корректно.

#### 7.1 Создать integration test

**Файл:** `tests/integration/orchestrator_api_test.go`

```go
package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	
	"github.com/stretchr/testify/assert"
	"github.com/vasenin26/agentmanager/internal/external"
	"github.com/vasenin26/agentmanager/internal/models"
	"go.uber.org/zap"
)

func TestTaskClientReserveWithAgentUUID(t *testing.T) {
	// Mock External API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tasks/next" {
			task := models.TaskDTO{
				ID:        "task-123",
				ProjectID: "project-456",
				ContextID: nil,
				Timeout:   300,
				PublicKey: nil,
			}
			json.NewEncoder(w).Encode(task)
			return
		}
		
		if r.URL.Path == "/tasks/task-123/reserve" {
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			
			// Проверить наличие agent_uuid
			assert.Contains(t, req, "agent_uuid")
			assert.Contains(t, req, "reserve_seconds")
			
			w.WriteHeader(http.StatusOK)
			response := map[string]string{
				"reserved_until": "2025-10-07T12:00:00Z",
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	
	// Создать TaskClient
	logger, _ := zap.NewDevelopment()
	client := external.NewTaskClient(server.URL, "test-token", 10*time.Second, logger)
	
	ctx := context.Background()
	
	// Получить задачу
	task, err := client.FetchTask(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, task)
	assert.Equal(t, "task-123", task.ID)
	
	// Зарезервировать задачу с agent_uuid
	agentUUID := "550e8400-e29b-41d4-a716-446655440000"
	err = client.ReserveTask(ctx, task.ID, 300, agentUUID)
	assert.NoError(t, err)
}

func TestTaskClientReservationConflict(t *testing.T) {
	// Mock External API с конфликтом
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tasks/task-123/reserve" {
			w.WriteHeader(http.StatusConflict)
			response := map[string]string{
				"error": "Task already reserved",
				"reserved_until": "2025-10-07T12:00:00Z",
			}
			json.NewEncoder(w).Encode(response)
			return
		}
	}))
	defer server.Close()
	
	logger, _ := zap.NewDevelopment()
	client := external.NewTaskClient(server.URL, "test-token", 10*time.Second, logger)
	
	ctx := context.Background()
	agentUUID := "550e8400-e29b-41d4-a716-446655440000"
	
	err := client.ReserveTask(ctx, "task-123", 300, agentUUID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already reserved")
}
```

#### 7.2 Создать unit test для `ProjectKeyManager`

**Файл:** `internal/ssh/project_keys_test.go`

```go
package ssh_test

import (
	"os"
	"path/filepath"
	"testing"
	
	"github.com/stretchr/testify/assert"
	"github.com/vasenin26/agentmanager/internal/ssh"
)

func TestProjectKeyManager_GenerateKeyPair(t *testing.T) {
	// Создать временную директорию
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	
	// Создать менеджер
	manager, err := ssh.NewProjectKeyManager(tmpDir)
	assert.NoError(t, err)
	
	// Генерировать ключи
	projectID := "project-123"
	privateKey, publicKey, err := manager.GenerateKeyPair(projectID)
	assert.NoError(t, err)
	assert.NotEmpty(t, privateKey)
	assert.NotEmpty(t, publicKey)
	
	// Проверить существование файлов
	privateKeyPath := filepath.Join(tmpDir, "projects", "project-123_private.pem")
	publicKeyPath := filepath.Join(tmpDir, "projects", "project-123_public.pub")
	
	assert.FileExists(t, privateKeyPath)
	assert.FileExists(t, publicKeyPath)
	
	// Проверить права доступа
	info, err := os.Stat(privateKeyPath)
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestProjectKeyManager_GetKeyPair(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	
	manager, err := ssh.NewProjectKeyManager(tmpDir)
	assert.NoError(t, err)
	
	projectID := "project-456"
	
	// Генерировать ключи
	originalPrivate, originalPublic, err := manager.GenerateKeyPair(projectID)
	assert.NoError(t, err)
	
	// Получить ключи
	retrievedPrivate, retrievedPublic, err := manager.GetKeyPair(projectID)
	assert.NoError(t, err)
	
	// Проверить соответствие
	assert.Equal(t, originalPrivate, retrievedPrivate)
	assert.Equal(t, originalPublic, retrievedPublic)
}

func TestProjectKeyManager_ValidatePublicKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)
	
	manager, err := ssh.NewProjectKeyManager(tmpDir)
	assert.NoError(t, err)
	
	projectID := "project-789"
	_, publicKey, err := manager.GenerateKeyPair(projectID)
	assert.NoError(t, err)
	
	// Валидный ключ
	isValid, err := manager.ValidatePublicKey(projectID, publicKey)
	assert.NoError(t, err)
	assert.True(t, isValid)
	
	// Невалидный ключ
	isValid, err = manager.ValidatePublicKey(projectID, "ssh-rsa AAAA...")
	assert.NoError(t, err)
	assert.False(t, isValid)
}
```

#### 7.3 Ручное тестирование с mock API

**Создать:** `tests/mock/orchestrator_api.py`

```python
#!/usr/bin/env python3
"""
Mock Orchestrator API для тестирования AgentManager
"""
from flask import Flask, request, jsonify
import datetime

app = Flask(__name__)

# In-memory хранилище задач
tasks = {
    "task-1": {
        "id": "task-1",
        "project_id": "project-100",
        "context_id": None,
        "timeout": 300,
        "public_key": None,
        "status": "wait",
        "reserved_until": None,
        "agent_uuid": None,
    },
    "task-2": {
        "id": "task-2",
        "project_id": "project-100",
        "context_id": "my-context",
        "timeout": 600,
        "public_key": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCtest...",
        "status": "wait",
        "reserved_until": None,
        "agent_uuid": None,
    }
}

projects = {
    "project-100": {
        "id": "project-100",
        "public_key": None,
    }
}

@app.route('/api/v1/orchestrator/tasks/next', methods=['GET'])
def get_next_task():
    """Получить следующую задачу"""
    # Найти первую свободную задачу
    for task_id, task in tasks.items():
        if task["status"] == "wait" and task["reserved_until"] is None:
            return jsonify({
                "id": task["id"],
                "project_id": task["project_id"],
                "context_id": task["context_id"],
                "timeout": task["timeout"],
                "public_key": task["public_key"],
            }), 200
    
    # Нет задач
    return '', 204

@app.route('/api/v1/orchestrator/tasks/<task_id>/reserve', methods=['POST'])
def reserve_task(task_id):
    """Зарезервировать задачу"""
    if task_id not in tasks:
        return jsonify({"error": "Task not found"}), 404
    
    task = tasks[task_id]
    
    # Проверить резервирование
    if task["reserved_until"] is not None:
        now = datetime.datetime.utcnow()
        reserved_until = datetime.datetime.fromisoformat(task["reserved_until"])
        if reserved_until > now:
            return jsonify({
                "error": "Task already reserved",
                "reserved_until": task["reserved_until"],
            }), 409
    
    # Получить данные запроса
    data = request.json
    reserve_seconds = data.get("reserve_seconds")
    agent_uuid = data.get("agent_uuid")
    
    if not reserve_seconds or not agent_uuid:
        return jsonify({"error": "Missing reserve_seconds or agent_uuid"}), 400
    
    # Зарезервировать задачу
    reserved_until = datetime.datetime.utcnow() + datetime.timedelta(seconds=reserve_seconds)
    task["reserved_until"] = reserved_until.isoformat() + "Z"
    task["agent_uuid"] = agent_uuid
    
    print(f"✅ Task {task_id} reserved by {agent_uuid} until {task['reserved_until']}")
    
    return jsonify({
        "reserved_until": task["reserved_until"],
    }), 200

@app.route('/api/v1/orchestrator/projects/<project_id>/key', methods=['PUT'])
def update_project_key(project_id):
    """Обновить публичный ключ проекта"""
    if project_id not in projects:
        return jsonify({"error": "Project not found"}), 404
    
    data = request.json
    public_key = data.get("public_key")
    
    if not public_key:
        return jsonify({"error": "Missing public_key"}), 400
    
    projects[project_id]["public_key"] = public_key
    
    print(f"✅ Project {project_id} public key updated")
    print(f"   Key: {public_key[:50]}...")
    
    return jsonify({"status": "updated"}), 200

if __name__ == '__main__':
    print("🚀 Starting Mock Orchestrator API on http://localhost:9000")
    print("📝 Available endpoints:")
    print("   GET  /api/v1/orchestrator/tasks/next")
    print("   POST /api/v1/orchestrator/tasks/{id}/reserve")
    print("   PUT  /api/v1/orchestrator/projects/{id}/key")
    app.run(host='0.0.0.0', port=9000, debug=True)
```

**Запуск:**
```bash
# Установить Flask
pip install flask

# Запустить mock API
python tests/mock/orchestrator_api.py

# В другом терминале - запустить AgentManager
TASK_API_URL=http://localhost:9000/api/v1/orchestrator \
TASK_API_TOKEN=test-token \
./bin/agentmanager
```

---

## Контрольный список (Checklist)

### Этап 1: Модель данных
- [ ] 1.1 Проверить соответствие `TaskDTO` спецификации External API

### Этап 2: Поддержка `agent_uuid`
- [ ] 2.1 Обновить `TaskClient.ReserveTask()` - добавить параметр `agentUUID`
- [ ] 2.2 Обновить payload в `ReserveTask()` - добавить поле `agent_uuid`
- [ ] 2.3 Добавить обработку 409 Conflict в `ReserveTask()`
- [ ] 2.4 Обновить `processTask()` - генерировать `agentUUID`
- [ ] 2.5 Добавить поле `AgentUUID` в `TaskDTO` (временное)

### Этап 3: SSH ключи проектов
- [ ] 3.1 Создать `internal/ssh/project_keys.go`
- [ ] 3.2 Реализовать `ProjectKeyManager`
- [ ] 3.3 Интегрировать `ProjectKeyManager` в `OrchestratorService`
- [ ] 3.4 Обновить `validateAndPrepareProjectKey()`
- [ ] 3.5 Обновить `generateAndRegisterProjectKey()`

### Этап 4: Передача данных в контейнер
- [ ] 4.1 Обновить сигнатуру `StartAgentForTask()` - добавить `agentUUID`
- [ ] 4.2 Добавить переменные окружения `AGENT_UUID` и `TASK_ID`
- [ ] 4.3 Обновить вызов `StartAgentForTask()` в `OrchestratorService`

### Этап 5: Обработка конфликтов
- [ ] 5.1 Добавить логику retry/skip при 409 Conflict
- [ ] 5.2 Добавить метрику `TaskReservationConflictsTotal`
- [ ] 5.3 Логировать конфликты резервирования

### Этап 6: Документация
- [ ] 6.1 Обновить `docs/task-api-specification.md`
- [ ] 6.2 Создать `docs/ssh-keys-architecture.md`
- [ ] 6.3 Обновить `README.md`

### Этап 7: Тестирование
- [ ] 7.1 Создать `tests/integration/orchestrator_api_test.go`
- [ ] 7.2 Создать `internal/ssh/project_keys_test.go`
- [ ] 7.3 Создать `tests/mock/orchestrator_api.py`
- [ ] 7.4 Протестировать с mock API
- [ ] 7.5 Протестировать с реальным External API

---

## Миграция и развертывание

### Backward Compatibility

Все изменения обратно совместимы:

✅ **Старые агенты** продолжат работать с ключами агентов  
✅ **Новые агенты** будут использовать ключи проектов  
✅ **Существующие задачи** без `context_id` продолжат работать  

### Переменные окружения (новые)

Добавить в `.env` и `docker-compose.yml`:

```bash
# Существующие (без изменений)
TASK_API_URL=https://api.example.com/api/v1/orchestrator
TASK_API_TOKEN=your-secret-token
TASK_API_TIMEOUT=10s
TASK_POLL_INTERVAL=5s

# SSH ключи (существующая, без изменений)
SSH_KEYS_DIR=./keys

# Новые переменные НЕ ТРЕБУЮТСЯ (всё работает через существующие)
```

### План развертывания

1. **Обновить код** - применить все изменения из плана
2. **Запустить тесты** - убедиться что всё работает
3. **Собрать новый образ** - `docker build -t agentmanager:v2.0.0 .`
4. **Обновить docker-compose.prod.yaml** - указать новый тег
5. **Развернуть на сервере** - `docker compose up -d`
6. **Мониторить логи** - проверить успешное резервирование задач
7. **Проверить метрики** - убедиться что конфликты резервирования обрабатываются

---

## Риски и митигация

### Риск 1: Конфликты резервирования

**Описание:** Несколько оркестраторов могут пытаться зарезервировать одну задачу.

**Митигация:**
- External API использует `FOR UPDATE SKIP LOCKED`
- AgentManager корректно обрабатывает 409 Conflict
- Метрика `TaskReservationConflictsTotal` для мониторинга

### Риск 2: Рассинхронизация SSH ключей

**Описание:** Ключи в AgentManager и External API могут различаться.

**Митигация:**
- Валидация `public_key` из API при каждой задаче
- Пересоздание ключей при несовпадении
- Логирование всех операций с ключами

### Риск 3: Утечка приватных ключей проектов

**Описание:** Приватные ключи проектов общие для всех агентов проекта.

**Митигация:**
- Права доступа 600 на файлы ключей
- Права доступа 700 на директорию
- Ключи не логируются
- Ключи не возвращаются через API

### Риск 4: Агент не вызывает `POST /api/agent/task`

**Описание:** Контейнер агента может не вызвать endpoint для получения задачи.

**Митигация:**
- Документация для разработчиков агента
- Timeout резервирования (автоматическое освобождение)
- Логирование событий запуска агента

---

## Заключение

Данный план описывает все необходимые изменения для полной интеграции AgentManager с внешним Orchestrator API.

**Ключевые изменения:**
1. ✅ Добавление `agent_uuid` в резервирование задач
2. ✅ Управление SSH ключами на уровне проектов
3. ✅ Передача контекста в контейнер агента
4. ✅ Обработка конфликтов резервирования
5. ✅ Обновление документации и тестов

**Оценка трудозатрат:**
- Разработка: 3-4 дня
- Тестирование: 1-2 дня
- Документация: 1 день
- **Итого: 5-7 дней**

**Следующие шаги:**
1. Утверждение плана
2. Создание feature branch
3. Последовательная реализация этапов 1-7
4. Code review
5. Тестирование на staging
6. Деплой в production

