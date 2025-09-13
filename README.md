# Agent Service (skeleton)

Описание: минимальный сервис для управления контейнерами-агентами.

API примеры (JSON):
- POST /agents — {"image":"registry/repo:tag","env":{"KEY":"VALUE"}}
- GET /agents — список запущенных агентов
- POST /agents/{id}/stop — остановить контейнер

Примечание: сервис поддерживает Prometheus-метрики на /metrics.
