# Task API Specification

## Обзор

AgentManager (оркестратор) работает в режиме pull-based и требует внешний Task API для получения задач и отправки результатов.

Этот документ описывает требования к внешнему Task API, который должен быть реализован на вашей стороне.

## Базовая информация

### Base URL
```
https://api.example.com/api/v1/orchestrator
```

Все endpoint'ы оркестратора находятся под префиксом `/api/v1/orchestrator`

### Аутентификация

Все запросы от оркестратора содержат Bearer token в заголовке:

```http
Authorization: Bearer {TASK_API_TOKEN}
```

Где `TASK_API_TOKEN` - значение из переменной окружения оркестратора.

### Content-Type

Все запросы и ответы используют JSON:

```http
Content-Type: application/json
```

## Endpoints

### 1. GET /api/v1/orchestrator/tasks/next

Получить следующую задачу для обработки.

#### Описание
Оркестратор вызывает этот endpoint с интервалом `TASK_POLL_INTERVAL` (по умолчанию каждые 5 секунд).

**Важно:** 
- Задача возвращается без резервирования (информационно)
- После получения оркестратор должен вызвать `POST /tasks/{id}/reserve` для подтверждения
- Оркестратор может делать несколько последовательных запросов для получения нескольких задач одновременно
- Агент самостоятельно отмечает задачу как выполненную через свой собственный API после завершения работы

#### Request

```http
GET /api/v1/orchestrator/tasks/next HTTP/1.1
Host: api.example.com
Authorization: Bearer your-token
```

**Нет тела запроса**

#### Response - Задача доступна (200 OK)

```http
HTTP/1.1 200 OK
Content-Type: application/json
```

```json
{
  "id": "task-123",
  "context_id": "context-456",
  "timeout": 300,
  "project_id": "project-789",
  "public_key": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC..."
}
```

**Поля:**

| Поле | Тип | Обязательно | Описание |
|------|-----|-------------|----------|
| `id` | string | ✅ Да | Уникальный идентификатор задачи |
| `context_id` | string \| null | ❌ Нет | ID контекста. Если задаче нужен Docker volume для сохранения состояния между задачами - укажите ID. Если null - агент запустится без volume |
| `timeout` | integer | ❌ Нет | Таймаут выполнения в секундах (информационное поле, оркестратор не использует) |
| `project_id` | string | ✅ Да | ID проекта, к которому относится задача. Используется для SSH ключей |
| `public_key` | string \| null | ❌ Нет | SSH публичный ключ проекта для валидации. Если null или отсутствует - оркестратор использует существующий ключ или сгенерирует новый |

#### Response - Нет задач (204 No Content)

```http
HTTP/1.1 204 No Content
```

**Нет тела ответа**

Это означает, что в данный момент нет задач для обработки. Оркестратор подождет `TASK_POLL_INTERVAL` и запросит снова.

#### Response - Ошибка (4xx, 5xx)

```http
HTTP/1.1 401 Unauthorized
Content-Type: application/json
```

```json
{
  "error": "Invalid token"
}
```

При ошибке оркестратор залогирует её и повторит запрос на следующей итерации.

---

### 2. POST /api/v1/orchestrator/tasks/{taskId}/reserve

Зарезервировать задачу с указанием времени блокировки.

#### Описание
После получения задачи через `GET /tasks/next`, оркестратор:
1. Анализирует задачу (есть ли `context_id`)
2. Прогнозирует время до запуска агента:
   - Без контекста: ~10 секунд
   - С контекстом: ~300 секунд (если контекст занят)
3. Отправляет подтверждение с резервированием

Если оркестратор не запустит агент в указанный срок, задача автоматически освобождается и может быть выдана другому оркестратору.

#### Request

```http
POST /api/v1/orchestrator/tasks/task-123/reserve HTTP/1.1
Host: api.example.com
Authorization: Bearer your-token
Content-Type: application/json
```

```json
{
  "reserve_seconds": 300
}
```

**Поля:**

| Поле | Тип | Обязательно | Описание |
|------|-----|-------------|----------|
| `reserve_seconds` | integer | ✅ Да | Время резервирования в секундах. Оркестратор гарантирует запуск агента в течение этого времени |

**Рекомендуемые значения:**
- Задача без контекста: `10` секунд
- Задача с контекстом (контекст свободен): `10` секунд
- Задача с контекстом (контекст занят): `300` секунд (5 минут)

#### Response - Успешное резервирование (200 OK)

```http
HTTP/1.1 200 OK
Content-Type: application/json
```

```json
{
  "reserved_until": "2025-10-06T15:30:00Z"
}
```

Или просто:

```http
HTTP/1.1 204 No Content
```

#### Response - Задача уже зарезервирована (409 Conflict)

```http
HTTP/1.1 409 Conflict
Content-Type: application/json
```

```json
{
  "error": "Task already reserved",
  "reserved_by": "orchestrator-2",
  "reserved_until": "2025-10-06T15:25:00Z"
}
```

Задача уже зарезервирована другим оркестратором или текущим оркестратором ранее.

#### Response - Задача не найдена (404 Not Found)

```http
HTTP/1.1 404 Not Found
Content-Type: application/json
```

```json
{
  "error": "Task not found"
}
```

---

### 3. PUT /api/v1/orchestrator/projects/{projectId}/key

Обновить SSH публичный ключ проекта.

#### Описание
Оркестратор вызывает этот endpoint когда генерирует новую пару SSH ключей для проекта.

Публичный ключ нужно сохранить в вашей системе (например, GitHub, GitLab) для доступа агента к репозиториям.

#### Request

```http
PUT /api/v1/orchestrator/projects/project-789/key HTTP/1.1
Host: api.example.com
Authorization: Bearer your-token
Content-Type: application/json
```

```json
{
  "public_key": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC8h3..."
}
```

**Поля:**

| Поле | Тип | Обязательно | Описание |
|------|-----|-------------|----------|
| `public_key` | string | ✅ Да | SSH публичный ключ в формате OpenSSH |

#### Response - Успех (200 OK или 204 No Content)

```http
HTTP/1.1 200 OK
Content-Type: application/json
```

```json
{
  "status": "updated"
}
```

Или просто:

```http
HTTP/1.1 204 No Content
```

#### Response - Ошибка (4xx, 5xx)

```http
HTTP/1.1 400 Bad Request
Content-Type: application/json
```

```json
{
  "error": "Invalid public key format"
}
```

При ошибке оркестратор залогирует её, но продолжит работу (ключ уже сохранен локально).

---

## Сценарии использования

### Сценарий 1: Новая задача без контекста

```
1. AgentManager → GET /api/v1/orchestrator/tasks/next
2. Task API → 200 OK
   {
     "id": "task-1",
     "context_id": null,
     "project_id": "project-1",
     "public_key": null
   }

3. AgentManager анализирует задачу:
   - Контекст не нужен (context_id = null)
   - Агент можно запустить сразу
   - Прогнозируемое время до запуска: 10 секунд

4. AgentManager → POST /api/v1/orchestrator/tasks/task-1/reserve
   {
     "reserve_seconds": 10
   }

5. Task API → 200 OK
   {
     "reserved_until": "2025-10-06T15:25:10Z"
   }

6. AgentManager:
   - Проверяет SSH ключи проекта
   - Если нет - генерирует новые
   - Вызывает PUT /api/v1/orchestrator/projects/project-1/key
   - Запускает агента без volume

7. Агент выполняет задачу
8. Агент завершается (exit 0) и самостоятельно отмечает задачу выполненной через свой API
```

### Сценарий 2: Задача с контекстом (контекст свободен)

```
1. AgentManager → GET /api/v1/orchestrator/tasks/next
2. Task API → 200 OK
   {
     "id": "task-2",
     "context_id": "my-project-context",
     "project_id": "project-1",
     "public_key": "ssh-rsa AAAA..."
   }

3. AgentManager анализирует задачу:
   - Нужен контекст "my-project-context"
   - Проверяет: контекст свободен ✅
   - Прогнозируемое время: 10 секунд

4. AgentManager → POST /api/v1/orchestrator/tasks/task-2/reserve
   {
     "reserve_seconds": 10
   }

5. Task API → 200 OK

6. AgentManager:
   - Проверяет существование контекста "my-project-context"
   - Если нет - создает Docker volume
   - Проверяет SSH ключи (валидирует public_key)
   - Занимает контекст
   - Запускает агента с volume

7. Агент выполняет задачу с доступом к /repos (volume)
8. Агент завершается (exit 0) и самостоятельно отмечает задачу выполненной

9. AgentManager освобождает контекст
10. AgentManager проверяет очередь задач для контекста
11. Если есть задачи - запускает следующую
```

### Сценарий 3: Задача с контекстом (контекст занят)

```
1. AgentManager → GET /api/v1/orchestrator/tasks/next
2. Task API → 200 OK
   {
     "id": "task-3",
     "context_id": "my-project-context",
     "project_id": "project-1",
     "public_key": "ssh-rsa AAAA..."
   }

3. AgentManager анализирует задачу:
   - Нужен контекст "my-project-context"
   - Проверяет: контекст занят ❌ (выполняется task-2)
   - В очереди контекста уже 2 задачи
   - Прогнозируемое время: ~300 секунд (5 минут)

4. AgentManager → POST /api/v1/orchestrator/tasks/task-3/reserve
   {
     "reserve_seconds": 300
   }

5. Task API → 200 OK
   {
     "reserved_until": "2025-10-06T15:30:00Z"
   }

6. AgentManager:
   - Добавляет task-3 в локальную очередь контекста "my-project-context"
   - Ждет освобождения контекста

7. Когда task-2 завершается:
   - AgentManager освобождает контекст
   - Проверяет очередь
   - Запускает task-3

8. Агент выполняет task-3
9. Агент завершается и отмечает задачу выполненной
```

### Сценарий 4: Нет задач

```
1. AgentManager → GET /api/v1/orchestrator/tasks/next
2. Task API → 204 No Content

3. AgentManager ждет 5 секунд (TASK_POLL_INTERVAL)
4. Повторяет запрос
```

### Сценарий 5: Получение нескольких задач одновременно

```
1. AgentManager проверяет доступную память (может запустить 3 агента)

2. AgentManager → GET /api/v1/orchestrator/tasks/next
3. Task API → 200 OK {"id": "task-1", "context_id": null, ...}

4. AgentManager → GET /api/v1/orchestrator/tasks/next
5. Task API → 200 OK {"id": "task-2", "context_id": null, ...}

6. AgentManager → GET /api/v1/orchestrator/tasks/next
7. Task API → 200 OK {"id": "task-3", "context_id": null, ...}

8. AgentManager → GET /api/v1/orchestrator/tasks/next
9. Task API → 204 No Content (нет больше задач)

10. AgentManager анализирует и резервирует задачи:
    - POST /tasks/task-1/reserve {"reserve_seconds": 10}
    - POST /tasks/task-2/reserve {"reserve_seconds": 10}
    - POST /tasks/task-3/reserve {"reserve_seconds": 10}

11. AgentManager запускает 3 агента параллельно для task-1, task-2, task-3
```

**Важно:** Каждая задача (task-1, task-2, task-3) уникальна.

### Сценарий 6: Оркестратор не успел запустить агент

```
1. AgentManager → GET /api/v1/orchestrator/tasks/next
2. Task API → 200 OK {"id": "task-1", ...}

3. AgentManager → POST /api/v1/orchestrator/tasks/task-1/reserve
   {"reserve_seconds": 10}
4. Task API → 200 OK {"reserved_until": "2025-10-06T15:25:10Z"}

5. AgentManager начинает подготовку (SSH ключи, контекст, ...)
6. Проходит 10 секунд, но агент еще не запущен (медленная подготовка)

7. Task API автоматически освобождает task-1 (истек reserve_seconds)

8. Другой оркестратор:
   - GET /tasks/next → получает task-1
   - POST /tasks/task-1/reserve → резервирует успешно
   - Запускает агента для task-1

9. Первый оркестратор пытается запустить агента:
   - Задача уже выполняется другим оркестратором
   - Оркестратор отменяет запуск
```

### Сценарий 7: Сбой агента

```
1. AgentManager → GET /api/v1/orchestrator/tasks/next
2. Task API → 200 OK {"id": "task-1", ...}

3. AgentManager → POST /api/v1/orchestrator/tasks/task-1/reserve
   {"reserve_seconds": 10}
4. Task API → 200 OK

5. AgentManager запускает агента
6. Агент выполняет задачу
7. Агент завершается с ошибкой (exit code != 0)

8. AgentManager:
   - Освобождает ресурсы
   - Агент не смог отметить задачу выполненной
   - Задача остается в статусе "reserved"

9. Task API:
   - По истечении timeout задачи (из поля "timeout" в TaskDTO)
   - Автоматически освобождает задачу
   - Задача становится доступной снова
```

---

## Требования к реализации

### 1. Управление задачами

- **Выдача:** `GET /tasks/next` возвращает задачу без резервирования (информационно)
- **Двухфазное резервирование:**
  1. Оркестратор получает задачу через `GET /tasks/next`
  2. Оркестратор анализирует задачу и прогнозирует время запуска
  3. Оркестратор резервирует задачу через `POST /tasks/{id}/reserve` с указанием `reserve_seconds`
- **Резервирование (reserve):** 
  - Оркестратор гарантирует запуск агента в течение `reserve_seconds`
  - Задача блокируется на указанное время
  - Если оркестратор не запустил агент вовремя - задача освобождается автоматически
- **Таймаут выполнения (timeout):** 
  - После запуска агента действует `timeout` из TaskDTO
  - Если задача не завершена в течение `timeout` секунд - освобождается
- **Завершение:** Агент отмечает задачу выполненной через свой собственный API
- **Множественные запросы:** Оркестратор может получать несколько задач, затем резервировать их пакетно

### 2. Rate Limiting

- Рекомендуется установить rate limit на `GET /tasks/next`
- Учтите, что оркестратор может делать **несколько запросов подряд** в одном цикле опроса
- Базовый интервал опроса: `TASK_POLL_INTERVAL` (по умолчанию 5 сек)
- Пример: оркестратор может сделать 5-10 запросов подряд каждые 5 секунд

### 3. Аутентификация

- Проверяйте Bearer token во всех запросах
- Возвращайте 401 Unauthorized если токен невалидный

### 4. Логирование

Рекомендуется логировать:
- Выдачу задач оркестратору
- Обновление SSH ключей
- Ошибки аутентификации

---

## Переменные окружения оркестратора

Оркестратор использует следующие переменные для Task API:

```bash
# URL базового API с префиксом оркестратора
TASK_API_URL=https://api.example.com/api/v1/orchestrator

# Токен аутентификации
TASK_API_TOKEN=your-secret-token

# Timeout для HTTP запросов (default: 10s)
TASK_API_TIMEOUT=10s

# Интервал опроса задач (default: 5s)
TASK_POLL_INTERVAL=5s
```

---

## Тестирование

### Тестирование с curl

```bash
# Получить задачу
curl -X GET "https://api.example.com/api/v1/orchestrator/tasks/next" \
  -H "Authorization: Bearer your-token"

# Зарезервировать задачу
curl -X POST "https://api.example.com/api/v1/orchestrator/tasks/task-123/reserve" \
  -H "Authorization: Bearer your-token" \
  -H "Content-Type: application/json" \
  -d '{"reserve_seconds":10}'

# Обновить SSH ключ проекта
curl -X PUT "https://api.example.com/api/v1/orchestrator/projects/project-789/key" \
  -H "Authorization: Bearer your-token" \
  -H "Content-Type: application/json" \
  -d '{"public_key":"ssh-rsa AAAA..."}'
```

### Mock API для разработки

Для локальной разработки можно использовать simple mock:

```bash
# docker-compose.override.yml
version: '3.8'
services:
  mock-api:
    image: mockserver/mockserver
    environment:
      MOCKSERVER_INITIALIZATION_JSON_PATH: /config/mock-api.json
    volumes:
      - ./mock-api.json:/config/mock-api.json
    ports:
      - "9000:1080"
  
  agent-svc:
    environment:
      - TASK_API_URL=http://mock-api:1080/api/v1/orchestrator
```

---

## Безопасность

### Рекомендации

1. **HTTPS**: Используйте HTTPS для production
2. **Токен**: Генерируйте криптографически стойкий токен
   ```bash
   openssl rand -base64 32
   ```
3. **Rate Limiting**: Ограничьте количество запросов
   - Учитывайте множественные последовательные запросы в одном цикле
   - Например: 20 запросов за 10 секунд вместо 1 запроса за 5 секунд
4. **IP Whitelist**: Разрешите доступ только с IP оркестратора
5. **Логирование**: Логируйте все запросы для аудита
6. **Резервирование задач**: Используйте транзакционную логику для предотвращения выдачи одной задачи дважды

---

## Troubleshooting

### Оркестратор не получает задачи

Проверьте:
1. `TASK_API_URL` корректный
2. Task API доступен
3. `TASK_API_TOKEN` валидный
4. Endpoint `/tasks/next` возвращает 200 или 204
5. Формат JSON корректный
6. Задачи не зарезервированы другими оркестраторами
7. Таймаут резервирования не слишком большой

### SSH ключи не обновляются

Проверьте:
1. Endpoint `/projects/{id}/key` реализован
2. Возвращает 200/204
3. Ключ сохраняется в вашей системе
4. Логи оркестратора на ошибки

### Одна и та же задача выдается несколько раз

Проверьте:
1. Реализован механизм резервирования задач
2. Задача помечается как "взята в работу" при выдаче через `/tasks/next`
3. Резервирование снимается только по таймауту или при завершении
4. В БД есть поля `status`, `reserved_at`, `reserved_by`

---

## Контракт API (OpenAPI)

```yaml
openapi: 3.0.0
info:
  title: Task API for AgentManager
  version: 1.0.0

servers:
  - url: https://api.example.com/api/v1/orchestrator
    description: Production API

security:
  - bearerAuth: []

paths:
  /tasks/next:
    get:
      summary: Get next task (without reservation)
      description: Returns next available task for orchestrator to analyze
      responses:
        '200':
          description: Task available
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Task'
        '204':
          description: No tasks available
        '401':
          description: Unauthorized

  /tasks/{taskId}/reserve:
    post:
      summary: Reserve task for execution
      description: Reserve task with estimated time until agent start
      parameters:
        - name: taskId
          in: path
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/ReserveRequest'
      responses:
        '200':
          description: Successfully reserved
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/ReserveResponse'
        '204':
          description: Successfully reserved
        '409':
          description: Task already reserved
        '404':
          description: Task not found
        '401':
          description: Unauthorized

  /projects/{projectId}/key:
    put:
      summary: Update project SSH key
      parameters:
        - name: projectId
          in: path
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/PublicKey'
      responses:
        '200':
          description: Success
        '204':
          description: Success
        '401':
          description: Unauthorized

components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer

  schemas:
    Task:
      type: object
      required:
        - id
        - project_id
      properties:
        id:
          type: string
          example: "task-123"
        context_id:
          type: string
          nullable: true
          example: "context-456"
        timeout:
          type: integer
          example: 300
        project_id:
          type: string
          example: "project-789"
        public_key:
          type: string
          nullable: true
          example: "ssh-rsa AAAAB3..."

    PublicKey:
      type: object
      required:
        - public_key
      properties:
        public_key:
          type: string
          example: "ssh-rsa AAAAB3NzaC1yc2EA..."

    ReserveRequest:
      type: object
      required:
        - reserve_seconds
      properties:
        reserve_seconds:
          type: integer
          description: Time in seconds until orchestrator will start agent
          example: 10

    ReserveResponse:
      type: object
      properties:
        reserved_until:
          type: string
          format: date-time
          example: "2025-10-06T15:30:00Z"
```

---

## Заключение

Это минимальный набор endpoints, необходимых для работы AgentManager в режиме pull-based оркестратора.

Все три endpoint'а обязательны:
- ✅ `GET /api/v1/orchestrator/tasks/next` - получение задач (без резервирования)
- ✅ `POST /api/v1/orchestrator/tasks/{id}/reserve` - резервирование задачи с указанием срока
- ✅ `PUT /api/v1/orchestrator/projects/{id}/key` - обновление SSH ключей

**Важно:** 
- Оркестратор использует **двухфазное резервирование**: сначала получает задачу, затем резервирует с прогнозом времени
- Агенты самостоятельно отмечают задачи как выполненные через свой собственный API
- Два таймаута: `reserve_seconds` (до запуска агента) и `timeout` (выполнение задачи)

**Важно:** Все endpoints должны быть доступны по пути `/api/v1/orchestrator`. Укажите полный URL в переменной окружения `TASK_API_URL`, например:
```bash
TASK_API_URL=https://api.example.com/api/v1/orchestrator
```

Реализуйте их на вашей стороне, и оркестратор сможет автоматически получать и обрабатывать задачи.

