# Command Proxy: как агент выполняет команды в DinD-песочнице

Command Proxy реализует протокол, описанный в `agentmodule/docs/command-proxy-protocol.md`
(контракт с агентом). Реализация: `internal/service/terminal_proxy.go`,
`internal/service/terminal_jobs.go`.

## Устройство

- Для каждого контекста (volume) оркестратор поднимает прокси, который слушает Unix-сокет на хосте
  `${ORCHESTRATOR_SOCKET_DIR:-/tmp/orchestrator}/{VOLUME_ID}.sock`.
- Сокет монтируется в контейнер агента по пути `/opt/terminal.sock`, путь передаётся агенту в
  переменной окружения `COMMAND_PROXY_SOCKET`.
- Команды выполняются в DinD-контейнере (`docker:dind`, privileged), который создаётся при первой
  команде. Volume контекста смонтирован в нём в `/opt/repos` (в контейнере агента тот же volume —
  `/home/local/context`).
- Каждая команда выполняется отдельным `sh -lc "<command>"` с stdin из `/dev/null`: пайпы и
  перенаправления поддерживаются, состояние shell (`cd`, переменные) между запросами не сохраняется —
  используйте `cwd`/`env` (для `start`) или `cd ... && ...`.
- Контейнер останавливается после 5 минут неактивности (если нет работающих job'ов) и удаляется
  после 7 дней неактивности.

## Правила транспорта

- **Один запрос = одно соединение.** Клиент пишет один JSON-объект (разделитель `\n` не нужен),
  сервер отвечает одной JSON-строкой, завершённой `\n`, и закрывает соединение.
- Если запрос не пришёл целиком за 10 секунд, сервер отвечает ошибкой `invalid_params`.
- Ошибки новых actions: `{"error": "<код>", "message": "..."}`, коды — `invalid_action`,
  `invalid_params`, `job_not_found`, `proxy_busy`. `exec`, `status`, `destroy` сохраняют плоский
  формат `{"error": "..."}`.

## Actions

| Action | Запрос | Ответ |
|---|---|---|
| `exec` | `command`, `timeout` (сек, по умолчанию 60, максимум 600) | `{"stdout","stderr","exit_code"}`; при таймауте команда убивается, `exit_code` = 124 |
| `start` | `command`, `cwd`, `env` (строки), `max_lifetime` (сек, по умолчанию 600, кап 3600, `<= 0` — ошибка) | `{"job_id"}` |
| `wait` | `job_id`, `timeout` (сек, по умолчанию 15, кап 30), `stdout_offset`, `stderr_offset` | `{"job_id","status","exit_code","stdout_delta","stdout_offset","stderr_delta","stderr_offset","truncated"}` |
| `peek` | как `wait`, без ожидания | как `wait` |
| `kill` | `job_id`, `signal` (`TERM` по умолчанию или `KILL`) | `{"job_id","status":"killed"\|"already_exited","exit_code"}` |
| `status` | — | `{"container_id","state","last_active_at"}` |
| `destroy` | — | `{"result":"ok"}`, контейнер удаляется |

Особенности job'ов:

- `status` у `wait`/`peek` — `running` или `exited`; для `running` `exit_code` = `null`.
  Ответ со статусом `exited` всегда содержит весь оставшийся вывод.
- Сигнал доставляется всему дереву процессов job'а (дерево замораживается `SIGSTOP` на время обхода
  `/proc`), так что фоновые подпроцессы команды тоже завершаются. Процессы, которые сами
  «демонизировались» (переродились к init), в дерево не попадают.
- По истечении `max_lifetime` дерево получает `SIGKILL` (`exit_code` = 137) независимо от того,
  опрашивает ли клиент job.
- Буфер вывода — 64 KB на поток; при переполнении отбрасываются старые данные, и в ответе, чей
  диапазон это затронуло, выставляется `truncated: true`. Для `exec` буфер — 256 KB на поток
  (сохраняется хвост вывода).
- Offset'ы — байтовые; дельта не обрывается посреди многобайтового UTF-8 символа.
- Одновременно может работать не больше 16 job'ов (иначе `proxy_busy`); завершённый job
  доступен для опроса ещё 10 минут, затем — `job_not_found`. Job'ы хранятся в памяти
  оркестратора и не переживают его перезапуск.

## Примеры (socat)

```bash
echo -n '{"action":"exec","command":"ls -la /opt/repos","timeout":30}' | socat - UNIX-CONNECT:/tmp/orchestrator/context-volume-1.sock
echo -n '{"action":"start","command":"make test","cwd":"/opt/repos"}' | socat - UNIX-CONNECT:/tmp/orchestrator/context-volume-1.sock
echo -n '{"action":"wait","job_id":"<id>","timeout":20,"stdout_offset":0,"stderr_offset":0}' | socat - UNIX-CONNECT:/tmp/orchestrator/context-volume-1.sock
echo -n '{"action":"kill","job_id":"<id>","signal":"KILL"}' | socat - UNIX-CONNECT:/tmp/orchestrator/context-volume-1.sock
```

PHP-клиент: `agentmodule/app/src/Application/Tools/Terminal/CommandProxyClient.php`.
