# Supernode HTTP Gateway

The HTTP gateway exposes the gRPC services via REST on port `8002` using grpc-gateway.

## Endpoints

### GET `/api/v1/status`
Returns supernode status: system resources (CPU, memory, storage), service info, and optionally P2P metrics.

- Query `include_p2p_metrics=true` enables detailed P2P metrics and peer info.
- When omitted or false, peer count, peer addresses, and `p2p_metrics` are not included.

Examples:

```bash
# Lightweight status
curl "http://localhost:8002/api/v1/status"

# Include P2P metrics and peer info
curl "http://localhost:8002/api/v1/status?include_p2p_metrics=true"
```

Example responses are shown in the main README under the SupernodeService section.


## API Documentation

- Swagger UI: `http://localhost:8002/swagger-ui/`
- OpenAPI Spec: `http://localhost:8002/swagger.json`

The Swagger UI provides an interactive interface to explore and test all available API endpoints.
