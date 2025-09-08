# Supernode HTTP Gateway

The Supernode exposes its gRPC services via an HTTP/JSON gateway on port `8002`.

- Swagger UI: http://localhost:8002/swagger-ui/
- OpenAPI Spec: http://localhost:8002/swagger.json

## Status API

GET `/api/v1/status`

Returns the current supernode status including system resources (CPU, memory, storage), running tasks, registered services, network info, and codec configuration.

- Query `include_p2p_metrics=true` adds detailed P2P metrics and peer information.

Example:
```bash
curl "http://localhost:8002/api/v1/status"
```

With P2P metrics:
```bash
curl "http://localhost:8002/api/v1/status?include_p2p_metrics=true"
```

## Services API

### GET `/api/v1/codec`
Returns the minimal effective RaptorQ codec configuration used by the node (fixed policy):

```json
{
  "symbol_size": 65535,
  "redundancy": 5,
  "max_memory_mb": 12288,
  "concurrency": 4,
  "headroom_pct": 10,
  "mem_limit_mb": 13653,
  "mem_limit_source": "cgroupv2:memory.max"
}
```

## API Documentation

Returns the list of available services and methods exposed by this supernode.

The Swagger UI provides an interactive interface to explore and test all available API endpoints.
