# Task Reservation Pattern

## Проблема

Оркестратор может делать **несколько последовательных запросов** к `GET /tasks/next` для получения нескольких задач одновременно. Без правильной реализации резервирования одна и та же задача может быть выдана дважды.

## Решение: Резервирование задач

### Схема базы данных

```sql
CREATE TABLE tasks (
    id VARCHAR(255) PRIMARY KEY,
    context_id VARCHAR(255),
    project_id VARCHAR(255) NOT NULL,
    public_key TEXT,
    timeout INT DEFAULT 300,
    
    -- Резервирование
    status VARCHAR(50) DEFAULT 'pending',  -- pending, reserved, completed, failed
    reserved_at TIMESTAMP NULL,
    reserved_by VARCHAR(255) NULL,
    reserved_until TIMESTAMP NULL,
    
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

CREATE INDEX idx_status ON tasks(status);
CREATE INDEX idx_reserved_until ON tasks(reserved_until);
```

### Алгоритм GET /tasks/next

```python
def get_next_task(orchestrator_id: str):
    """
    Получить следующую задачу с автоматическим резервированием
    """
    with database.transaction():
        # 1. Освободить задачи с истекшим резервированием
        now = datetime.now()
        database.execute("""
            UPDATE tasks 
            SET status = 'pending', 
                reserved_at = NULL, 
                reserved_by = NULL,
                reserved_until = NULL
            WHERE status = 'reserved' 
              AND reserved_until < %s
        """, [now])
        
        # 2. Получить следующую доступную задачу
        task = database.execute("""
            SELECT * FROM tasks 
            WHERE status = 'pending'
            ORDER BY created_at ASC
            LIMIT 1
            FOR UPDATE SKIP LOCKED  -- Важно для конкурентного доступа!
        """).fetchone()
        
        if not task:
            return None  # 204 No Content
        
        # 3. Зарезервировать задачу
        timeout = task.timeout or 300
        reserved_until = now + timedelta(seconds=timeout)
        
        database.execute("""
            UPDATE tasks 
            SET status = 'reserved',
                reserved_at = %s,
                reserved_by = %s,
                reserved_until = %s
            WHERE id = %s
        """, [now, orchestrator_id, reserved_until, task.id])
        
        # 4. Вернуть задачу
        return {
            "id": task.id,
            "context_id": task.context_id,
            "project_id": task.project_id,
            "public_key": task.public_key,
            "timeout": task.timeout
        }
```

### Ключевые моменты

1. **`FOR UPDATE SKIP LOCKED`** (PostgreSQL)
   - Блокирует строку для UPDATE
   - `SKIP LOCKED` - пропускает уже заблокированные строки
   - Предотвращает race condition при параллельных запросах

2. **Транзакция**
   - Весь процесс (SELECT + UPDATE) в одной транзакции
   - Гарантирует атомарность операции

3. **Автоматическое освобождение**
   - Перед выборкой проверяем истекшие резервирования
   - Альтернатива: cron-задача или background worker

4. **Идентификатор оркестратора**
   - `reserved_by` позволяет отслеживать, какой оркестратор взял задачу
   - Полезно для debugging и мониторинга

### Пример для разных БД

#### PostgreSQL

```sql
-- Освободить истекшие
UPDATE tasks 
SET status = 'pending', reserved_at = NULL, reserved_by = NULL, reserved_until = NULL
WHERE status = 'reserved' AND reserved_until < NOW();

-- Получить и зарезервировать
UPDATE tasks 
SET status = 'reserved',
    reserved_at = NOW(),
    reserved_by = 'orchestrator-1',
    reserved_until = NOW() + INTERVAL '300 seconds'
WHERE id = (
    SELECT id FROM tasks 
    WHERE status = 'pending'
    ORDER BY created_at ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;
```

#### MySQL

```sql
-- Освободить истекшие
UPDATE tasks 
SET status = 'pending', reserved_at = NULL, reserved_by = NULL, reserved_until = NULL
WHERE status = 'reserved' AND reserved_until < NOW();

-- Получить и зарезервировать (с блокировкой)
START TRANSACTION;

SELECT * FROM tasks 
WHERE status = 'pending'
ORDER BY created_at ASC
LIMIT 1
FOR UPDATE;

UPDATE tasks 
SET status = 'reserved',
    reserved_at = NOW(),
    reserved_by = 'orchestrator-1',
    reserved_until = DATE_ADD(NOW(), INTERVAL timeout SECOND)
WHERE id = ?;

COMMIT;
```

#### MongoDB

```javascript
// Используем findAndModify для атомарной операции
db.tasks.findAndModify({
    query: {
        $or: [
            { status: 'pending' },
            { 
                status: 'reserved', 
                reserved_until: { $lt: new Date() } 
            }
        ]
    },
    sort: { created_at: 1 },
    update: {
        $set: {
            status: 'reserved',
            reserved_at: new Date(),
            reserved_by: 'orchestrator-1',
            reserved_until: new Date(Date.now() + 300000)
        }
    },
    new: true
});
```

## Множественные запросы от оркестратора

### Сценарий

```
Оркестратор имеет 2GB памяти, лимит на агента - 512MB
→ Может запустить 4 агента одновременно
→ Делает 4 последовательных запроса к GET /tasks/next
```

### Что происходит в БД

```
Запрос 1:
  SELECT ... WHERE status='pending' → task-1
  UPDATE task-1 SET status='reserved' ✅
  
Запрос 2 (через 100ms):
  SELECT ... WHERE status='pending' → task-2 (task-1 уже reserved!)
  UPDATE task-2 SET status='reserved' ✅
  
Запрос 3 (через 200ms):
  SELECT ... WHERE status='pending' → task-3
  UPDATE task-3 SET status='reserved' ✅
  
Запрос 4 (через 300ms):
  SELECT ... WHERE status='pending' → task-4
  UPDATE task-4 SET status='reserved' ✅
```

**Результат:** 4 уникальные задачи зарезервированы

### Параллельные оркестраторы

Если у вас несколько оркестраторов, `FOR UPDATE SKIP LOCKED` гарантирует, что они не получат одну и ту же задачу:

```
Orchestrator-1 (запрос в 10:00:00.000):
  → Блокирует task-1
  → Обновляет task-1
  → Возвращает task-1

Orchestrator-2 (запрос в 10:00:00.050):
  → Пытается заблокировать task-1 (уже заблокирована!)
  → SKIP LOCKED → переходит к task-2
  → Блокирует task-2
  → Обновляет task-2
  → Возвращает task-2
```

## Завершение задачи

Агент отмечает задачу выполненной через свой собственный API:

```sql
UPDATE tasks 
SET status = 'completed',
    completed_at = NOW()
WHERE id = :task_id 
  AND status = 'reserved';
```

Или при ошибке:

```sql
UPDATE tasks 
SET status = 'failed',
    failed_at = NOW(),
    error_message = :error
WHERE id = :task_id 
  AND status = 'reserved';
```

## Мониторинг

### Полезные запросы

```sql
-- Сколько задач в каждом статусе
SELECT status, COUNT(*) 
FROM tasks 
GROUP BY status;

-- Задачи с истекшим резервированием
SELECT * FROM tasks 
WHERE status = 'reserved' 
  AND reserved_until < NOW();

-- Средняя продолжительность резервирования
SELECT AVG(TIMESTAMPDIFF(SECOND, reserved_at, COALESCE(completed_at, NOW())))
FROM tasks 
WHERE reserved_at IS NOT NULL;

-- Какой оркестратор взял больше всего задач
SELECT reserved_by, COUNT(*) 
FROM tasks 
WHERE status = 'reserved' 
GROUP BY reserved_by;
```

### Метрики для Prometheus

```python
from prometheus_client import Counter, Gauge, Histogram

tasks_reserved_total = Counter('tasks_reserved_total', 'Total tasks reserved')
tasks_completed_total = Counter('tasks_completed_total', 'Total tasks completed')
tasks_failed_total = Counter('tasks_failed_total', 'Total tasks failed')
tasks_expired_total = Counter('tasks_expired_total', 'Total reservations expired')

tasks_pending = Gauge('tasks_pending', 'Number of pending tasks')
tasks_reserved = Gauge('tasks_reserved', 'Number of reserved tasks')

reservation_duration = Histogram('task_reservation_duration_seconds', 
                                'Task reservation duration')
```

## Best Practices

### 1. Таймаут резервирования

Используйте реалистичные таймауты:
- Короткие задачи (< 5 мин): timeout = 300s (5 мин)
- Средние задачи (5-30 мин): timeout = 1800s (30 мин)
- Длинные задачи (> 30 мин): timeout = 7200s (2 часа)

### 2. Cleanup задач

Периодически очищайте завершенные задачи:

```sql
-- Удалить задачи старше 7 дней
DELETE FROM tasks 
WHERE status IN ('completed', 'failed') 
  AND updated_at < NOW() - INTERVAL 7 DAY;
```

### 3. Повторные попытки

Для failed задач можно добавить механизм retry:

```sql
ALTER TABLE tasks ADD COLUMN retry_count INT DEFAULT 0;
ALTER TABLE tasks ADD COLUMN max_retries INT DEFAULT 3;

-- При ошибке
UPDATE tasks 
SET status = 'pending',  -- Вернуть в очередь
    retry_count = retry_count + 1,
    reserved_at = NULL,
    reserved_by = NULL,
    reserved_until = NULL
WHERE id = :task_id 
  AND retry_count < max_retries;
```

### 4. Приоритеты

Добавьте приоритеты для задач:

```sql
ALTER TABLE tasks ADD COLUMN priority INT DEFAULT 0;

-- В запросе
SELECT * FROM tasks 
WHERE status = 'pending'
ORDER BY priority DESC, created_at ASC
LIMIT 1
FOR UPDATE SKIP LOCKED;
```

## Резюме

✅ **Обязательно:**
- Используйте транзакции
- Блокировка строк (`FOR UPDATE`)
- Атомарное обновление статуса
- Таймаут резервирования

✅ **Рекомендуется:**
- `SKIP LOCKED` для конкурентного доступа
- Автоматическое освобождение истекших резервирований
- Мониторинг и метрики
- Cleanup старых задач

❌ **Избегайте:**
- SELECT без блокировки + UPDATE отдельно (race condition!)
- Резервирование без таймаута
- Отсутствие индексов на `status` и `reserved_until`

---

**Помните:** Правильное резервирование критически важно, когда оркестратор делает несколько запросов подряд!

