# Инструкция: как агент отправляет команды в Unix-сокет Command Proxy

Эта инструкция описывает, как агент должен отправлять команды в Unix-сокет, чтобы выполнить их в DinD-контейнере через Command Proxy Service.

**Ключевая идея**
- Оркестратор создаёт для каждого контекста (volume) Unix-сокет на хосте: `/var/run/orchestrator/{VOLUME_ID}.sock`.
- При запуске контейнера агента конкретный сокет файл монтируется из хоста в контейнер агента по пути `/opt/terminal` (bind mount).
- Агенту в его окружение передаётся переменная `COMMAND_PROXY_SOCKET=/opt/terminal`, указывающая путь к сокету внутри контейнера.
- Агент открывает соединение по Unix-сокету, отправляет один JSON-запрос и ждёт JSON-ответ.

Поддерживаемые `action`:
- `exec` — выполнить команду: `{"action":"exec","command":"ls /shared-data"}`
- `status` — получить статус контейнера: `{"action":"status"}`
- `destroy` — удалить контейнер: `{"action":"destroy"}`

Формат ответа для `exec`:
```json
{ "stdout": "...", "stderr": "...", "exit_code": 0 }
```
Если произошла внутренняя ошибка — вернётся объект `{ "error": "описание" }`.

---

## Общие правила
- Подключение: AF_UNIX (Unix domain socket).
- Отправьте ровно один JSON-объект; сервер обработает и вернёт ответ, затем закроет соединение.
- Сервер выполняет команду через `sh -lc "<command>"` внутри DinD-контейнера; поддерживаются пайпы и перенаправления.
- Таймаут выполнения по умолчанию ~60s (конфигурируемо на стороне сервера).

---

## Примеры CLI

Вручную (с `socat`):

```bash
echo '{"action":"exec","command":"ls -la /shared-data"}' | socat - UNIX-CONNECT:/var/run/orchestrator/context-volume-1.sock

echo '{"action":"status"}' | socat - UNIX-CONNECT:/var/run/orchestrator/context-volume-1.sock

echo '{"action":"destroy"}' | socat - UNIX-CONNECT:/var/run/orchestrator/context-volume-1.sock
```

(Внутри контейнера агента сокет доступен по пути `/opt/terminal`, который указан в переменной окружения `COMMAND_PROXY_SOCKET`.)

---

## Пример на PHP (рекомендуемый способ для агентов на PHP)

Ниже два варианта: синхронный — чтение до EOF, и вариант с ограничением времени ожидания.

### Простой пример (чтение до EOF)

```php
<?php
function call_proxy_exec(string $socketPath, string $command) {
    $req = json_encode(['action' => 'exec', 'command' => $command]);

    $errNo = 0; $errStr = '';
    $uri = "unix://" . $socketPath;
    $fp = stream_socket_client($uri, $errNo, $errStr, 2);
    if (!$fp) {
        throw new \RuntimeException("Failed to connect to socket: $errStr ($errNo)");
    }

    // Send request
    fwrite($fp, $req);

    // Read response until EOF (server closes connection after sending)
    $response = '';
    while (!feof($fp)) {
        $chunk = fread($fp, 4096);
        if ($chunk === false) break;
        $response .= $chunk;
    }
    fclose($fp);

    $obj = json_decode($response, true);
    if ($obj === null) {
        throw new \RuntimeException('Invalid JSON response from proxy: ' . $response);
    }

    if (isset($obj['error'])) {
        throw new \RuntimeException('Proxy error: ' . $obj['error']);
    }

    return $obj; // ['stdout'=>'...', 'stderr'=>'...', 'exit_code'=>0]
}

// Usage
try {
    $socket = getenv('COMMAND_PROXY_SOCKET') ?: '/opt/terminal';
    $res = call_proxy_exec($socket, 'ls -la /shared-data');
    echo "STDOUT:\n" . $res['stdout'] . "\n";
    echo "STDERR:\n" . $res['stderr'] . "\n";
    echo "EXIT CODE: " . $res['exit_code'] . "\n";
} catch (Exception $e) {
    echo "Error: " . $e->getMessage() . "\n";
}
```

### Пример с таймаутом чтения (Read deadline)

```php
<?php
function call_proxy_exec_timeout(string $socketPath, string $command, int $readTimeoutSeconds = 70) {
    $req = json_encode(['action' => 'exec', 'command' => $command]);

    $uri = "unix://" . $socketPath;
    $fp = stream_socket_client($uri, $errNo, $errStr, 2);
    if (!$fp) {
        throw new \RuntimeException("Failed to connect to socket: $errStr ($errNo)");
    }

    // Write request
    stream_set_blocking($fp, true);
    fwrite($fp, $req);

    // Set read timeout
    stream_set_timeout($fp, $readTimeoutSeconds);

    $response = '';
    while (!feof($fp)) {
        $chunk = fread($fp, 4096);
        if ($chunk === false) break;
        $response .= $chunk;
        // Check for timeout
        $info = stream_get_meta_data($fp);
        if ($info['timed_out']) {
            fclose($fp);
            throw new \RuntimeException('Read timed out');
        }
    }
    fclose($fp);

    $obj = json_decode($response, true);
    if ($obj === null) {
        throw new \RuntimeException('Invalid JSON response from proxy: ' . $response);
    }
    if (isset($obj['error'])) {
        throw new \RuntimeException('Proxy error: ' . $obj['error']);
    }
    return $obj;
}

// Usage
try {
    $socket = getenv('COMMAND_PROXY_SOCKET') ?: '/opt/terminal';
    $res = call_proxy_exec_timeout($socket, 'cat /shared-data/somefile.txt', 70);
    echo $res['stdout'];
} catch (Exception $e) {
    echo "Error: " . $e->getMessage() . "\n";
}
```

---

## Обработка ошибок и практические советы
- Всегда проверяйте наличие `COMMAND_PROXY_SOCKET` в окружении контейнера агента.
- Сервер может вернуть `{ "error":"..." }` — обрабатывайте это отдельно.
- Читайте ответ до EOF, так как сервер закрывает соединение после отправки ответа.
- Ограничьте время подключения/чтения (timeout) чтобы не блокироваться навсегда.
- Для больших выводов может потребоваться поблочная обработка (streaming) и аккумулирование чанков.

---

## Вариант: добавить клиент-утилиту в проект
Если хотите, я могу добавить небольшой Go-клиент в `internal/client/proxy_client.go`, который предоставляет удобную обёртку для `exec`, `status`, `destroy`. Напишите, если добавить.

---

Файл: `AGENT_SOCKET_USAGE.md` создан автоматически.
