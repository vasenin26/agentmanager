# Orchestrator Configuration

## Environment Variables

The following environment variables configure the pull-based orchestrator:

### Bolt Database

```bash
# Path to bbolt database file
BOLT_DB_PATH=./data/orchestrator.db
```

### External Task API

```bash
# URL of the external task API (with orchestrator prefix)
TASK_API_URL=https://api.example.com/api/v1/orchestrator

# Authentication token for the task API
TASK_API_TOKEN=your-api-token

# Timeout for task API requests
TASK_API_TIMEOUT=10s

# Interval between task polling requests
TASK_POLL_INTERVAL=5s
```

### Agent Memory Management

```bash
# Memory limit per agent in megabytes
AGENT_MEMORY_LIMIT_MB=512
```

### Agent Configuration

```bash
# API token for all agents (shared token)
AGENT_API_TOKEN=your-agent-api-token
```

**Note:** The orchestrator is always enabled and requires `TASK_API_URL` to be set.

## Database Structure

The orchestrator uses bbolt (embedded key-value database) with the following buckets:

### Buckets

1. **contexts** - Stores context information
   - Key: contextID
   - Value: JSON(ContextDTO)

2. **agent_states** - Stores running agent states
   - Key: agentID
   - Value: JSON(AgentStateDTO)

3. **context_queues** - Stores task queues for contexts
   - Key: contextID
   - Value: JSON([]ContextQueueItem)

## External Task API

The orchestrator expects the following API endpoints under the `/api/v1/orchestrator` prefix:

### GET /api/v1/orchestrator/tasks/next

Fetch the next available task (without reservation).

**Response:**
- `200 OK` - Task available
- `204 No Content` - No tasks available

**Response Body (200):**
```json
{
  "id": "task-123",
  "context_id": "context-456",  // optional, null if no context required
  "timeout": 300,                // timeout in seconds
  "project_id": "project-789",   // required, project identifier
  "public_key": "ssh-rsa AAAA..." // optional, SSH public key for validation
}
```

### POST /api/v1/orchestrator/tasks/{taskId}/reserve

Reserve task with estimated time until agent start.

**Request Body:**
```json
{
  "reserve_seconds": 10  // 10 seconds for tasks without context, 300 for tasks with busy context
}
```

**Response:**
- `200 OK` or `204 No Content` - Successfully reserved
- `409 Conflict` - Task already reserved by another orchestrator
- `404 Not Found` - Task not found

### PUT /api/v1/orchestrator/projects/{projectId}/key

Update project SSH public key. Called when orchestrator generates new keys for a project.

**Request Body:**
```json
{
  "public_key": "ssh-rsa AAAAB3NzaC1yc2EA..."
}
```

**Response:**
- `200 OK` or `204 No Content` - Success

## Metrics

The orchestrator exports the following Prometheus metrics:

### Counters
- `orchestrator_tasks_fetched_total` - Total tasks fetched from external API
- `orchestrator_tasks_processed_total` - Total tasks processed successfully
- `orchestrator_tasks_failed_total` - Total tasks that failed
- `orchestrator_contexts_created_total` - Total contexts created

### Gauges
- `orchestrator_contexts_active` - Number of currently active contexts
- `orchestrator_context_queue_length{context_id}` - Length of task queue per context
- `orchestrator_available_memory_bytes` - Available memory in bytes
- `orchestrator_used_memory_bytes` - Used memory by agents in bytes
- `orchestrator_active_agents` - Number of currently active agents

## How It Works

### Task Processing Flow

1. **Task Polling Loop**
   - Orchestrator polls `{TASK_API_URL}/tasks/next` every `TASK_POLL_INTERVAL`
   - Can make multiple sequential requests to fetch multiple tasks at once
   - Checks available memory before fetching tasks
   - Fetches tasks until memory is full or no more tasks available (204 response)
   - If no memory available, waits for next interval

2. **Task Reservation (Two-Phase)**
   - Phase 1: Get task via `GET /tasks/next` (informational)
   - Phase 2: Analyze task and estimate time until agent start
     - No context needed: 10 seconds
     - Context needed but free: 10 seconds
     - Context needed but busy: 300 seconds (5 minutes)
   - Phase 3: Reserve task via `POST /tasks/{id}/reserve` with `reserve_seconds`
   - If orchestrator doesn't start agent within `reserve_seconds`, task is released automatically

3. **Task Without Context**
   - Reserves for 10 seconds
   - Creates agent immediately
   - Starts container without volume mount
   - Passes `TASK_ID` environment variable to agent

4. **Task With Context**
   - Gets or creates Docker volume for context
   - If context is available:
     - Reserves for 10 seconds
     - Occupies context
     - Creates agent with volume mounted at `/repos`
     - Starts container
   - If context is occupied:
     - Reserves for 300 seconds (estimated queue wait time)
     - Enqueues task in context's local queue
     - Waits for context to be released

5. **Agent Completion**
   - Docker events listener detects container exit
   - If exit code is 0 (success):
     - Releases memory
     - Agent marks task as completed in external API via its own API
     - If context has queued tasks:
       - Starts next task from queue
     - Otherwise:
       - Releases context
   - If exit code is non-zero (failure):
     - Releases memory and context
     - Agent didn't mark task as completed
     - Task becomes available again in external API after timeout

### Context Management

- Each context has a unique Docker volume
- Volume ID: `context-{contextID}`
- Volume is mounted at `/repos` in agent containers
- Context can only be used by one agent at a time
- Context persists across multiple tasks
- Queued tasks wait for context to be released

### Memory Management

- Total system memory is retrieved from Docker
- Each agent has a memory limit (`AGENT_MEMORY_LIMIT_MB`)
- Used memory is tracked in bbolt database
- New tasks are only fetched if memory is available

## Deployment

### Docker Compose

The orchestrator is automatically started when:
- `ORCHESTRATOR_ENABLED=true` (default)
- `TASK_API_URL` is set

Volume `orchestrator_data` persists the bbolt database across restarts.

### Graceful Shutdown

On SIGINT/SIGTERM:
1. Stop task polling loop
2. Stop Docker event listener
3. Close database connections
4. Shutdown HTTP server

Running agents continue until completion.

## Troubleshooting

### Orchestrator Not Starting

Check logs for:
- `Orchestrator is enabled, initializing...`
- `Orchestrator started`

If disabled, check:
- `ORCHESTRATOR_ENABLED` is not `false` or `0`
- `TASK_API_URL` is set

### Tasks Not Being Processed

Check:
- External API is accessible
- `TASK_API_TOKEN` is valid
- Available memory: `orchestrator_available_memory_bytes` metric
- Task API returns tasks
- Task reservation mechanism is working correctly
- Each call to `/tasks/next` returns a unique task

### Context Issues

Check:
- Docker volumes are created: `docker volume ls`
- Context state in database
- Queue lengths: `orchestrator_context_queue_length` metric

### Memory Issues

Check:
- `orchestrator_available_memory_bytes` metric
- `orchestrator_used_memory_bytes` metric
- `AGENT_MEMORY_LIMIT_MB` setting
- Number of active agents

