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

GET `/api/v1/services`

Returns the list of available services and methods exposed by this supernode.

Example:
```bash
curl http://localhost:8002/api/v1/services
```

## Notes

- The gateway translates between HTTP/JSON and gRPC/protobuf, enabling easy integration with web tooling and monitoring.
- Interactive exploration is available via Swagger UI.
