# Two-Phase Task Reservation

## Концепция

Оркестратор использует **двухфазное резервирование задач** для оптимального управления очередями и предотвращения блокировки задач.

## Фазы резервирования

### Фаза 1: Получение задачи (GET)

```
GET /api/v1/orchestrator/tasks/next
```

- Задача возвращается **без резервирования**
- Это информационный запрос
- Оркестратор может получить несколько задач подряд
- Каждая задача уникальна

**Ответ:**
```json
{
  "id": "task-123",
  "context_id": "my-context", 
  "project_id": "project-1",
  "timeout": 300
}
```

### Фаза 2: Анализ и прогнозирование

Оркестратор анализирует задачу и прогнозирует время до запуска агента:

| Ситуация | Время (reserve_seconds) |
|----------|------------------------|
| Задача без контекста | 10 секунд |
| Задача с контекстом (контекст свободен) | 10 секунд |
| Задача с контекстом (контекст занят) | 300 секунд (5 минут) |

**Логика прогнозирования в оркестраторе:**

```go
func (os *OrchestratorService) estimateTimeUntilStart(task *TaskDTO) int {
    const baseTimeSeconds = 10  // Базовое время подготовки
    
    if task.ContextID == nil {
        return baseTimeSeconds  // Нет контекста - быстрый старт
    }
    
    if !contextExists || !contextOccupied {
        return baseTimeSeconds  // Контекст свободен - быстрый старт
    }
    
    return 300  // Контекст занят - ждем в очереди
}
```

### Фаза 3: Резервирование (POST)

```
POST /api/v1/orchestrator/tasks/{taskId}/reserve
```

**Запрос:**
```json
{
  "reserve_seconds": 10
}
```

- Оркестратор **гарантирует** запуск агента в течение `reserve_seconds`
- Задача блокируется для этого оркестратора
- Другие оркестраторы не могут взять эту задачу

**Ответ 200 OK:**
```json
{
  "reserved_until": "2025-10-06T15:30:00Z"
}
```

**Ответ 409 Conflict:**
```json
{
  "error": "Task already reserved",
  "reserved_by": "orchestrator-2"
}
```

## Два типа таймаутов

### 1. Reserve Timeout (reserve_seconds)

- **Назначение:** Время до запуска агента
- **Кто устанавливает:** Оркестратор
- **Что происходит при истечении:** Задача освобождается и может быть выдана другому оркестратору
- **Типичные значения:** 10-300 секунд

### 2. Execution Timeout (timeout)

- **Назначение:** Время выполнения задачи агентом
- **Кто устанавливает:** Внешняя система (в TaskDTO)
- **Что происходит при истечении:** Задача считается проваленной и освобождается
- **Типичные значения:** 300-7200 секунд

## Пример: Задача без контекста

```
1. Оркестратор → GET /tasks/next
2. API → 200 OK
   {
     "id": "task-1",
     "context_id": null,  // Нет контекста
     "project_id": "project-1"
   }

3. Оркестратор анализирует:
   - Контекст не нужен
   - Можно запустить сразу
   - Время подготовки: ~10 секунд

4. Оркестратор → POST /tasks/task-1/reserve
   {
     "reserve_seconds": 10
   }

5. API → 200 OK
   {
     "reserved_until": "2025-10-06T15:25:10Z"
   }

6. Оркестратор:
   - Готовит SSH ключи (2 сек)
   - Создает Docker контейнер (3 сек)
   - Запускает агента (1 сек)
   - Итого: ~6 секунд (укладывается в 10)

7. Агент выполняет задачу
8. Агент отмечает задачу выполненной
```

## Пример: Задача с занятым контекстом

```
1. Оркестратор → GET /tasks/next
2. API → 200 OK
   {
     "id": "task-2",
     "context_id": "my-context",  // Нужен контекст
     "project_id": "project-1"
   }

3. Оркестратор анализирует:
   - Нужен контекст "my-context"
   - Проверяет: контекст занят (выполняется task-1)
   - В очереди контекста уже 2 задачи
   - Прогноз: ~300 секунд (5 минут)

4. Оркестратор → POST /tasks/task-2/reserve
   {
     "reserve_seconds": 300
   }

5. API → 200 OK

6. Оркестратор:
   - Помещает task-2 в локальную очередь контекста
   - Ждет освобождения контекста

7. Когда контекст освобождается:
   - Оркестратор запускает task-2
   - (Укладывается в 300 секунд резервирования)

8. Агент выполняет задачу
9. Агент отмечает задачу выполненной
```

## Пример: Оркестратор не успел

```
1. Оркестратор-1 → GET /tasks/next
2. API → 200 OK {"id": "task-1"}

3. Оркестратор-1 → POST /tasks/task-1/reserve
   {"reserve_seconds": 10}

4. API → 200 OK {"reserved_until": "2025-10-06T15:25:10Z"}

5. Оркестратор-1 начинает подготовку...
   - Генерация SSH ключей занимает 15 секунд (медленно!)

6. Проходит 10 секунд → reserve_seconds истек

7. API автоматически освобождает task-1

8. Оркестратор-2 → GET /tasks/next
9. API → 200 OK {"id": "task-1"}  // Та же задача!

10. Оркестратор-2 → POST /tasks/task-1/reserve
    {"reserve_seconds": 10}

11. API → 200 OK

12. Оркестратор-2 успешно запускает task-1

13. Оркестратор-1 завершает подготовку и пытается запустить агента
    - Обнаруживает, что задача уже выполняется
    - Отменяет запуск
```

## Преимущества двухфазного резервирования

### 1. Гибкое управление очередями

- Оркестратор может получить несколько задач
- Проанализировать их все
- Зарезервировать оптимально

### 2. Предотвращение блокировок

- Если оркестратор "завис" - задача автоматически освобождается через `reserve_seconds`
- Не нужен длинный глобальный timeout

### 3. Честная конкуренция

- Несколько оркестраторов могут конкурировать за задачи
- Кто быстрее зарезервировал - тот и выполняет

### 4. Оптимизация для контекстов

- Короткое резервирование для быстрых задач
- Длинное резервирование для задач в очереди контекста

## Требования к реализации API

### Хранение состояния задачи

```sql
CREATE TABLE tasks (
    id VARCHAR(255) PRIMARY KEY,
    status VARCHAR(50),           -- pending, reserved, completed, failed
    
    -- Reserve timeout
    reserved_at TIMESTAMP NULL,
    reserved_by VARCHAR(255) NULL,
    reserved_until TIMESTAMP NULL,
    reserve_seconds INT NULL,
    
    -- Execution timeout
    started_at TIMESTAMP NULL,
    timeout INT DEFAULT 300,
    
    -- Other fields
    context_id VARCHAR(255),
    project_id VARCHAR(255)
);
```

### Логика GET /tasks/next

```python
def get_next_task():
    """
    Возвращает следующую доступную задачу БЕЗ резервирования
    """
    # Просто выбираем pending задачу
    task = db.query("""
        SELECT * FROM tasks 
        WHERE status = 'pending'
        ORDER BY created_at ASC
        LIMIT 1
    """)
    
    return task  # Не меняем статус!
```

### Логика POST /tasks/{id}/reserve

```python
def reserve_task(task_id, reserve_seconds, orchestrator_id):
    """
    Резервирует задачу с указанием времени
    """
    with db.transaction():
        task = db.query("""
            SELECT * FROM tasks 
            WHERE id = %s
            FOR UPDATE
        """, [task_id])
        
        if task.status != 'pending':
            return 409  # Already reserved
        
        now = datetime.now()
        reserved_until = now + timedelta(seconds=reserve_seconds)
        
        db.execute("""
            UPDATE tasks 
            SET status = 'reserved',
                reserved_at = %s,
                reserved_by = %s,
                reserved_until = %s,
                reserve_seconds = %s
            WHERE id = %s
        """, [now, orchestrator_id, reserved_until, reserve_seconds, task_id])
        
        return 200
```

### Автоматическое освобождение

```python
# Cron job или background worker каждые 5 секунд
def auto_release_expired_reservations():
    """
    Освобождает задачи с истекшим reserve_seconds
    """
    db.execute("""
        UPDATE tasks 
        SET status = 'pending',
            reserved_at = NULL,
            reserved_by = NULL,
            reserved_until = NULL,
            reserve_seconds = NULL
        WHERE status = 'reserved' 
          AND reserved_until < NOW()
    """)
```

## Мониторинг

### Метрики для Prometheus

```python
# Сколько задач зарезервировано
tasks_reserved_total = Counter('tasks_reserved_total')

# Распределение reserve_seconds
reserve_seconds_histogram = Histogram('task_reserve_seconds')

# Сколько задач освободилось по таймауту
reserve_timeouts_total = Counter('task_reserve_timeouts_total')

# Средняя разница между reserve_seconds и фактическим временем запуска
reserve_accuracy = Histogram('task_reserve_accuracy_seconds')
```

### Полезные запросы

```sql
-- Задачи с истекшим резервированием
SELECT * FROM tasks 
WHERE status = 'reserved' 
  AND reserved_until < NOW();

-- Средний reserve_seconds по типам задач
SELECT 
    CASE WHEN context_id IS NULL THEN 'no-context' ELSE 'with-context' END AS task_type,
    AVG(reserve_seconds) AS avg_reserve_seconds
FROM tasks 
WHERE reserved_at IS NOT NULL
GROUP BY task_type;

-- Точность прогнозов (насколько reserve_seconds был правильным)
SELECT 
    AVG(TIMESTAMPDIFF(SECOND, reserved_at, started_at)) AS actual_seconds,
    AVG(reserve_seconds) AS estimated_seconds
FROM tasks 
WHERE started_at IS NOT NULL;
```

## Резюме

✅ **Двухфазное резервирование:**
1. GET /tasks/next - получить задачу
2. Analyze - спрогнозировать время
3. POST /tasks/{id}/reserve - зарезервировать

✅ **Два таймаута:**
- `reserve_seconds` - до запуска агента
- `timeout` - выполнение задачи

✅ **Автоматическое освобождение:**
- По истечении `reserve_seconds`
- По истечении `timeout`

✅ **Преимущества:**
- Гибкость
- Отказоустойчивость
- Оптимизация для контекстов

