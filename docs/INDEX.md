# Индекс документации: Интеграция с Orchestrator API

## 🚀 Начало работы

| Документ | Описание | Время чтения |
|----------|----------|--------------|
| **[../QUICK_START.md](../QUICK_START.md)** | ⭐ Начните здесь! Быстрый старт | 5 мин |
| **[../INTEGRATION_CHANGES_SUMMARY.md](../INTEGRATION_CHANGES_SUMMARY.md)** | Сводка всех изменений | 10 мин |

## 📖 Основная документация

### Краткая сводка (обязательно к прочтению)

| Документ | Описание | Время чтения |
|----------|----------|--------------|
| **[integration-summary.md](integration-summary.md)** | Краткая техническая сводка изменений | 15 мин |
| **[ssh-keys-architecture.md](ssh-keys-architecture.md)** | ⭐ Архитектура SSH ключей на уровне проектов | 30 мин |

### Визуальные материалы

| Документ | Описание | Время чтения |
|----------|----------|--------------|
| **[architecture-comparison.md](architecture-comparison.md)** | Диаграммы архитектуры до/после | 30 мин |

### Технический план

| Документ | Описание | Время чтения |
|----------|----------|--------------|
| **[integration-plan.md](integration-plan.md)** | Полный технический план реализации (1250+ строк) | 2 часа |

## 📚 Существующая документация

### Конфигурация и спецификации

| Документ | Описание |
|----------|----------|
| **[task-api-specification.md](task-api-specification.md)** | Спецификация Task API (текущая) |
| **[orchestrator-configuration.md](orchestrator-configuration.md)** | Конфигурация оркестратора |
| **[task-reservation-pattern.md](task-reservation-pattern.md)** | Паттерн резервирования задач |

### Общее

| Документ | Описание |
|----------|----------|
| **[../README.md](../README.md)** | Основная документация проекта |

## 🎯 Маршруты чтения

### Для быстрого ознакомления (30 минут)

1. [QUICK_START.md](../QUICK_START.md) - 5 мин
2. [INTEGRATION_CHANGES_SUMMARY.md](../INTEGRATION_CHANGES_SUMMARY.md) - 10 мин
3. [integration-summary.md](integration-summary.md) - 15 мин

### Для понимания архитектуры (2 часа)

1. [QUICK_START.md](../QUICK_START.md) - 5 мин
2. [integration-summary.md](integration-summary.md) - 15 мин
3. [ssh-keys-architecture.md](ssh-keys-architecture.md) - 30 мин ⭐ **Важно!**
4. [architecture-comparison.md](architecture-comparison.md) - 30 мин
5. [integration-plan.md](integration-plan.md) - просмотр структуры - 30 мин

### Для полной реализации (1 день)

1. Все документы из раздела "Для понимания архитектуры"
2. [integration-plan.md](integration-plan.md) - детальное изучение - 2 часа
3. Чтение кода существующей реализации - 2 часа
4. Планирование реализации - 1 час

## 🔑 Ключевые концепции

### SSH ключи на уровне проектов

**Главная концепция:**
- Ключи генерируются **оркестратором** (не агентами!)
- **Один раз** для всего проекта
- **Передаются всем агентам** проекта

**Подробно:** [ssh-keys-architecture.md](ssh-keys-architecture.md)

### agent_uuid

**UUID воркера** для идентификации в External API

**Подробно:** [integration-summary.md](integration-summary.md#agent_uuid)

### Двухфазное резервирование

1. GET /tasks/next - получение информации
2. POST /tasks/{id}/reserve - резервирование с `agent_uuid`

**Подробно:** [architecture-comparison.md](architecture-comparison.md#двухфазная-модель-резервирования)

## 📁 Структура файлов проекта

### Документация

```
docs/
├── INDEX.md                          ← Вы здесь
├── integration-summary.md            ← Краткая сводка
├── integration-plan.md               ← Полный план
├── architecture-comparison.md        ← Диаграммы
├── ssh-keys-architecture.md          ← SSH ключи (важно!)
├── task-api-specification.md         ← Существующая спецификация
├── orchestrator-configuration.md     ← Существующая конфигурация
└── task-reservation-pattern.md       ← Существующий паттерн
```

### Новые файлы (создать)

```
internal/ssh/
├── project_keys.go          ← Управление SSH ключами проектов
└── project_keys_test.go     ← Тесты

tests/
├── integration/
│   └── orchestrator_api_test.go  ← Integration тесты
└── mock/
    └── orchestrator_api.py       ← Mock API
```

### Изменяемые файлы

```
internal/
├── external/
│   └── task_client.go           ← +agent_uuid
├── service/
│   ├── orchestrator_service.go  ← +ProjectKeyManager
│   └── agent_service.go         ← +TASK_ID, +AGENT_UUID
├── models/
│   └── task.go                  ← +AgentUUID
└── metrics/
    └── metrics.go               ← +конфликты резервирования
```

## 🎓 Учебные материалы

### Внешняя документация (docmodule/plane)

Документация External API (Laravel/PHP реализация):

| Документ | Описание |
|----------|----------|
| `docmodule/plane/orchestrator-api-technical-plan.md` | Технический план External API |
| `docmodule/docs/ORCHESTRATOR_API_FAQ.md` | FAQ по External API |
| `docmodule/ORCHESTRATOR_API_IMPLEMENTATION_SUMMARY.md` | Сводка реализации |

### Код для изучения

**Рекомендуемый порядок:**

1. `internal/models/task.go` - модели данных
2. `internal/external/task_client.go` - клиент Task API
3. `internal/service/orchestrator_service.go` - основная логика
4. `internal/service/agent_service.go` - управление агентами

## 📊 Визуальные материалы

### Диаграммы в документах

- **[architecture-comparison.md](architecture-comparison.md)** - архитектура до/после
  - Сравнение текущей и новой архитектуры
  - Workflow резервирования задач
  - Обработка конфликтов
  - Жизненный цикл задачи

- **[ssh-keys-architecture.md](ssh-keys-architecture.md)** - SSH ключи
  - Структура хранения ключей
  - Workflow генерации и регистрации
  - Сценарии использования
  - Примеры кода

## 🔍 Поиск по темам

### SSH ключи
- [ssh-keys-architecture.md](ssh-keys-architecture.md) - полная архитектура
- [integration-plan.md](integration-plan.md#этап-3-управление-ssh-ключами-проектов) - реализация
- [QUICK_START.md](../QUICK_START.md#ключевая-концепция-ssh-ключей) - краткое описание

### agent_uuid
- [integration-summary.md](integration-summary.md#agent_uuid) - описание
- [integration-plan.md](integration-plan.md#этап-2-добавление-поддержки-agent_uuid) - реализация
- [architecture-comparison.md](architecture-comparison.md#обработка-конфликтов) - использование

### Резервирование задач
- [task-reservation-pattern.md](task-reservation-pattern.md) - существующий паттерн
- [architecture-comparison.md](architecture-comparison.md#двухфазная-модель-резервирования) - новый паттерн
- [integration-plan.md](integration-plan.md#этап-2) - реализация

### Конфликты (409 Conflict)
- [architecture-comparison.md](architecture-comparison.md#обработка-конфликтов) - визуализация
- [integration-plan.md](integration-plan.md#этап-5-обработка-конфликтов) - реализация
- [integration-summary.md](integration-summary.md#обработка-конфликтов) - краткое описание

## 📞 Помощь и поддержка

### Часто задаваемые вопросы

**Q: С чего начать?**  
A: Читайте [QUICK_START.md](../QUICK_START.md)

**Q: Как работают SSH ключи проектов?**  
A: Подробно в [ssh-keys-architecture.md](ssh-keys-architecture.md)

**Q: Где полный план реализации?**  
A: [integration-plan.md](integration-plan.md)

**Q: Где визуальные диаграммы?**  
A: [architecture-comparison.md](architecture-comparison.md)

### Troubleshooting

Если возникли проблемы, проверьте:

1. [integration-plan.md - Troubleshooting](integration-plan.md#troubleshooting)
2. [ssh-keys-architecture.md - FAQ](ssh-keys-architecture.md#faq)
3. [architecture-comparison.md - FAQ](architecture-comparison.md#faq)

## ✅ Checklist для начинающих

Перед началом работы убедитесь, что:

- [ ] Прочитали [QUICK_START.md](../QUICK_START.md)
- [ ] Прочитали [INTEGRATION_CHANGES_SUMMARY.md](../INTEGRATION_CHANGES_SUMMARY.md)
- [ ] Изучили [integration-summary.md](integration-summary.md)
- [ ] **Поняли концепцию SSH ключей** из [ssh-keys-architecture.md](ssh-keys-architecture.md)
- [ ] Просмотрели диаграммы в [architecture-comparison.md](architecture-comparison.md)
- [ ] Понимаете что ключи генерируются **оркестратором**, не агентами
- [ ] Готовы читать [integration-plan.md](integration-plan.md)

## 🎯 Цель интеграции

Интеграция AgentManager (Go) с внешним Orchestrator API (Laravel/PHP).

**Основные изменения:**
1. Резервирование задач с `agent_uuid`
2. SSH ключи на уровне проектов (генерируются оркестратором)
3. Передача контекста агенту (`TASK_ID`, `AGENT_UUID`)

**Результат:**
- ✅ Полная совместимость с External API
- ✅ Масштабируемая архитектура SSH ключей
- ✅ Корректная обработка конфликтов резервирования

---

**Дата создания:** 2025-10-06  
**Версия:** 1.0  
**Статус:** ✅ Документация завершена

**Следующий шаг:** Читайте [QUICK_START.md](../QUICK_START.md) →

