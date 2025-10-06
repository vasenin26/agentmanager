# Архитектура SSH ключей на уровне проектов

## Обзор

AgentManager использует **SSH ключи на уровне проектов**, где:
- Ключи генерируются **оркестратором один раз** для каждого проекта
- Приватный ключ **передается ВСЕМ агентам** этого проекта
- Публичный ключ **регистрируется в External API** для GitHub/GitLab

## Основные принципы

### 1. Один проект = один ключ

```
Project #100:
  ├── SSH Key Pair (generated once by Orchestrator)
  │   ├── Private Key → передается всем агентам проекта
  │   └── Public Key → регистрируется в GitHub/GitLab
  │
  └── Agents (могут быть запущены параллельно):
      ├── Agent #1 → использует project-100_private.pem
      ├── Agent #2 → использует project-100_private.pem
      └── Agent #3 → использует project-100_private.pem
```

### 2. Генерация ключей оркестратором

**Кто генерирует:** Оркестратор (AgentManager)  
**Когда:** При получении первой задачи для нового проекта  
**Где хранится:** Файловая система оркестратора (`keys/projects/`)

```go
// Workflow генерации ключей
func (os *OrchestratorService) processTask(ctx context.Context, task *models.TaskDTO) error {
    // 1. Получили задачу для project_id = "project-100"
    
    // 2. Проверить существование ключей проекта
    if !projectKeyManager.KeyPairExists("project-100") {
        // 3. Ключей нет - генерируем ОДИН РАЗ для всего проекта
        privateKey, publicKey := projectKeyManager.GenerateKeyPair("project-100")
        
        // 4. Сохраняем локально
        // keys/projects/project-100_private.pem
        // keys/projects/project-100_public.pub
        
        // 5. Отправляем публичный ключ в External API
        taskClient.UpdateProjectPublicKey(ctx, "project-100", publicKey)
        // External API → сохраняет в БД → регистрирует в GitHub
    }
    
    // 6. Получаем приватный ключ для передачи агенту
    privateKey, _ := projectKeyManager.GetKeyPair("project-100")
    
    // 7. Запускаем агента с приватным ключом ПРОЕКТА
    agentService.StartAgentForTask(
        taskID,
        agentUUID,
        privateKey, // ← Ключ ПРОЕКТА, не агента!
    )
}
```

### 3. Передача ключей агентам

**Все агенты одного проекта получают ОДИНАКОВЫЙ приватный ключ:**

```
┌─────────────────────────────────────────────────────────┐
│               Orchestrator (AgentManager)               │
│                                                         │
│  keys/projects/project-100_private.pem  (stored once)   │
│                                                         │
└────────────┬────────────────┬───────────────┬───────────┘
             │                │               │
             ▼                ▼               ▼
     ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
     │  Agent #1    │  │  Agent #2    │  │  Agent #3    │
     │              │  │              │  │              │
     │ SSH_PRIVATE_ │  │ SSH_PRIVATE_ │  │ SSH_PRIVATE_ │
     │ KEY=<same>   │  │ KEY=<same>   │  │ KEY=<same>   │
     │              │  │              │  │              │
     │ project-100  │  │ project-100  │  │ project-100  │
     └──────────────┘  └──────────────┘  └──────────────┘
           │                  │                 │
           └──────────────────┴─────────────────┘
                              │
                              ▼
                    ┌──────────────────┐
                    │   GitHub/GitLab  │
                    │                  │
                    │ Registered:      │
                    │ public_key       │
                    │ (project-100)    │
                    └──────────────────┘
```

## Структура хранения

### Файловая система оркестратора

```
keys/
└── projects/
    ├── project-100_private.pem  (права: 600)
    ├── project-100_public.pub   (права: 644)
    ├── project-200_private.pem  (права: 600)
    └── project-200_public.pub   (права: 644)
```

**Права доступа:**
- `projects/` директория: **700** (только владелец)
- `*_private.pem`: **600** (только владелец, чтение/запись)
- `*_public.pub`: **644** (владелец: чтение/запись, остальные: чтение)

### База данных External API

```sql
-- Таблица projects
CREATE TABLE projects (
    id BIGINT PRIMARY KEY,
    name VARCHAR(255),
    public_key TEXT,  ← Публичный ключ проекта
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);
```

## Workflow генерации и регистрации

### Сценарий 1: Первая задача нового проекта

```
Time  Orchestrator                     External API               GitHub
──────────────────────────────────────────────────────────────────────────
T0    GET /orchestrator/tasks/next
      ← task-123, project_id=project-100

T1    Check: KeyPairExists("project-100")?
      → NO

T2    GenerateKeyPair("project-100")
      ├─ Generate RSA 2048-bit key
      ├─ Save private: keys/projects/project-100_private.pem
      └─ Save public: keys/projects/project-100_public.pub

T3    PUT /orchestrator/projects/project-100/key
      Body: {"public_key": "ssh-rsa AAAAB3..."}
                                          │
                                          ▼
                                    Save to DB:
                                    projects.public_key
                                          │
                                          ▼
                                    Register in GitHub:
                                    Deploy key added
                                                              ✅ Key registered

T4    Start Agent #1
      Env: SSH_PRIVATE_KEY=<project-100_private.pem>

T5    Agent #1: git clone using SSH_PRIVATE_KEY
                                                              ← Authenticated ✅
```

### Сценарий 2: Вторая задача того же проекта

```
Time  Orchestrator                     External API               GitHub
──────────────────────────────────────────────────────────────────────────
T0    GET /orchestrator/tasks/next
      ← task-456, project_id=project-100

T1    Check: KeyPairExists("project-100")?
      → YES ✅

T2    GetKeyPair("project-100")
      ← keys/projects/project-100_private.pem
      (ключи УЖЕ сгенерированы ранее)

T3    ⚠️  NO API call!
      (публичный ключ уже зарегистрирован)
                                          
T4    Start Agent #2
      Env: SSH_PRIVATE_KEY=<project-100_private.pem>
      (ТОЖЕ САМЫЙ ключ, что и у Agent #1!)

T5    Agent #2: git clone using SSH_PRIVATE_KEY
                                                              ← Authenticated ✅
      (тот же ключ, работает!)
```

### Сценарий 3: Параллельные агенты одного проекта

```
Time  Orchestrator                     Agents
─────────────────────────────────────────────────────────────
T0    Task #1 (project-100) → Start Agent #1
      SSH_PRIVATE_KEY=<project-100_private.pem>
                                        ├─ Agent #1 ─┐
                                        │  git clone │
                                        │            │

T1    Task #2 (project-100) → Start Agent #2
      SSH_PRIVATE_KEY=<project-100_private.pem>
                                        ├─ Agent #2 ─┤
                                        │  git clone │ Оба используют
                                        │            │ ОДИН ключ!

T2    Task #3 (project-100) → Start Agent #3
      SSH_PRIVATE_KEY=<project-100_private.pem>
                                        ├─ Agent #3 ─┤
                                        │  git clone │
                                        │            │
                                        └────────────┘
```

## Реализация

### ProjectKeyManager

```go
package ssh

type ProjectKeyManager struct {
    keysDir string // keys/projects/
}

// GenerateKeyPair генерирует ключи ОДИН РАЗ для проекта
func (m *ProjectKeyManager) GenerateKeyPair(projectID string) (privateKey, publicKey string, error) {
    // 1. Генерируем RSA 2048-bit ключ
    key := rsa.GenerateKey(rand.Reader, 2048)
    
    // 2. Кодируем в PEM формат
    privateKeyPEM := pem.EncodeToMemory(...)
    
    // 3. Кодируем публичный ключ в OpenSSH формат
    publicKeySSH := ssh.MarshalAuthorizedKey(...)
    
    // 4. Сохраняем в файловую систему
    os.WriteFile(projectID+"_private.pem", privateKeyPEM, 0600)
    os.WriteFile(projectID+"_public.pub", publicKeySSH, 0644)
    
    return privateKeyPEM, publicKeySSH, nil
}

// GetKeyPair получает существующие ключи проекта
func (m *ProjectKeyManager) GetKeyPair(projectID string) (privateKey, publicKey string, error) {
    privateKey := os.ReadFile(projectID + "_private.pem")
    publicKey := os.ReadFile(projectID + "_public.pub")
    return privateKey, publicKey, nil
}

// KeyPairExists проверяет существование ключей
func (m *ProjectKeyManager) KeyPairExists(projectID string) bool {
    _, err := os.Stat(projectID + "_private.pem")
    return err == nil
}
```

### Интеграция в OrchestratorService

```go
func (os *OrchestratorService) startAgentForTask(ctx context.Context, task *models.TaskDTO) error {
    // 1. Проверить/получить ключи проекта
    privateKey, publicKey, err := os.validateAndPrepareProjectKey(ctx, task)
    
    // 2. Передать приватный ключ ПРОЕКТА агенту
    agentService.StartAgentForTask(
        taskID,
        agentUUID,
        privateKey, // ← Ключ проекта, общий для всех агентов
    )
}

func (os *OrchestratorService) validateAndPrepareProjectKey(ctx context.Context, task *models.TaskDTO) (string, string, error) {
    projectID := task.ProjectID
    
    if os.projectKeyManager.KeyPairExists(projectID) {
        // Ключи уже существуют - используем их
        return os.projectKeyManager.GetKeyPair(projectID)
    }
    
    // Ключей нет - генерируем ОДИН РАЗ для проекта
    return os.generateAndRegisterProjectKey(ctx, projectID)
}

func (os *OrchestratorService) generateAndRegisterProjectKey(ctx context.Context, projectID string) (string, string, error) {
    // 1. Генерируем ключи проекта
    privateKey, publicKey, _ := os.projectKeyManager.GenerateKeyPair(projectID)
    
    // 2. Регистрируем публичный ключ в External API
    os.taskClient.UpdateProjectPublicKey(ctx, projectID, publicKey)
    
    // 3. Возвращаем приватный ключ для передачи агенту
    return privateKey, publicKey, nil
}
```

## Безопасность

### Угрозы и митигация

| Угроза | Митигация |
|--------|-----------|
| Утечка приватного ключа | - Права 600 на файлы<br>- Директория с правами 700<br>- Не логируется<br>- Не возвращается через API |
| Перехват при передаче агенту | - Передача через переменную окружения Docker<br>- Изолированная сеть контейнеров |
| Несанкционированный доступ к репозиторию | - Публичный ключ регистрируется только для конкретного проекта<br>- Read-only deploy keys в GitHub |
| Компрометация одного агента | - Все агенты проекта используют один ключ<br>- При компрометации - ротация ключа для всего проекта |

### Ротация ключей

**Сценарий: Компрометация ключа проекта**

```bash
# 1. Удалить старые ключи
rm keys/projects/project-100_private.pem
rm keys/projects/project-100_public.pub

# 2. При следующей задаче оркестратор:
# - Обнаружит отсутствие ключей
# - Сгенерирует новую пару
# - Зарегистрирует новый публичный ключ в External API

# 3. External API обновит ключ в GitHub
# - Удалит старый deploy key
# - Добавит новый deploy key

# 4. Все новые агенты получат новый ключ
```

## Преимущества архитектуры

### ✅ Масштабируемость

**Проблема (старая архитектура):**
```
100 агентов × 1 ключ = 100 SSH ключей в GitHub
```

**Решение (новая архитектура):**
```
100 агентов одного проекта × 1 ключ = 1 SSH ключ в GitHub
```

### ✅ Управляемость

**Проблема:** Как отозвать доступ 50 агентов к репозиторию?

**Решение:** Удалить один deploy key проекта в GitHub.

### ✅ Простота ротации

**Проблема:** Нужно обновить ключи - как?

**Решение:**
1. Удалить `project-100_*.pem`
2. Следующая задача → автоматическая генерация и регистрация новых ключей

### ✅ Совместимость

Все агенты проекта могут работать параллельно:
- С одним репозиторием
- Используя один SSH ключ
- Без конфликтов

## Сравнение с альтернативами

### Альтернатива 1: Ключи на уровне агента

```
❌ 1000 агентов → 1000 SSH ключей в GitHub
❌ Сложная ротация (обновить 1000 ключей)
❌ Лимиты GitHub на количество deploy keys
❌ Сложный аудит доступа
```

### Альтернатива 2: Один глобальный ключ

```
❌ Все проекты используют один ключ
❌ Нельзя отозвать доступ для одного проекта
❌ Компрометация ключа = компрометация всех проектов
```

### Текущее решение: Ключи на уровне проекта

```
✅ Один ключ на проект (масштабируемость)
✅ Изолированный доступ между проектами
✅ Простая ротация (один ключ вместо множества)
✅ Гибкое управление доступом
```

## Мониторинг

### Метрики

```go
// Количество проектов с зарегистрированными ключами
project_keys_registered_total

// Ошибки генерации ключей
project_keys_generation_errors_total

// Ошибки регистрации в External API
project_keys_registration_errors_total
```

### Логирование

```go
// Генерация ключей
log.Info("Generating NEW SSH key pair for project",
    zap.String("projectID", projectID))

// Использование существующих ключей
log.Info("Using existing project SSH keys",
    zap.String("projectID", projectID))

// Регистрация в API
log.Info("Successfully registered project public key",
    zap.String("projectID", projectID),
    zap.String("publicKeyPrefix", publicKey[:50]+"..."))
```

## FAQ

**Q: Что если два агента одновременно попытаются сгенерировать ключи для нового проекта?**

A: Race condition защищен на уровне файловой системы:
```go
if _, err := os.Stat(privateKeyPath); err == nil {
    // Файл уже существует - используем его
    return getExistingKey()
}
// Генерируем только если файла нет
```

**Q: Можно ли использовать разные ключи для разных окружений (dev/prod)?**

A: Да, используйте разные `projectID`:
```
project-100-dev  → keys/projects/project-100-dev_*.pem
project-100-prod → keys/projects/project-100-prod_*.pem
```

**Q: Что если приватный ключ утерян?**

A: Удалите файлы ключей проекта. При следующей задаче оркестратор сгенерирует новую пару и зарегистрирует её автоматически.

**Q: Как агент использует ключ для git операций?**

A: Агент получает `SSH_PRIVATE_KEY` в переменной окружения и настраивает SSH:
```bash
# Внутри контейнера агента
echo "$SSH_PRIVATE_KEY" > ~/.ssh/id_rsa
chmod 600 ~/.ssh/id_rsa
git clone git@github.com:org/repo.git
```

---

**Дата создания:** 2025-10-06  
**Версия:** 1.0  
**Статус:** ✅ Утверждено

