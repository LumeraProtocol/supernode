## Lumera Client (Slim Guide)

A minimal guide to the Lumera client

What it is

- Lightweight client over gRPC with small modules: `Auth`, `Action`, `ActionMsg`, `SuperNode`, `Tx`, `Node`.
- Shared tx pipeline for building, simulating, signing, and broadcasting messages.

Create a client

```go
cfg, _ := lumera.NewConfig(
  "https://grpc.testnet.lumera.io", // or host:port
  "chain-id",
  keyName,   // key name in your keyring
  keyring,   // cosmos-sdk keyring
)
cli, _ := lumera.NewClient(ctx, cfg)
defer cli.Close()
```

Using modules

- `cli.Action()` – query actions (GetAction, GetActionFee, GetParams)
- `cli.ActionMsg()` – send action messages (see below)
- `cli.Auth()` – accounts/verify
- `cli.SuperNode()` – supernode queries
- `cli.Tx()` – tx internals (shared by helpers)
- `cli.Node()` – chain/node info

Gas and fees (tx module)

- Default gas price: `0.025 ulume`.
- Accepts gas price as `"0.025"` or `"0.025ulume"`.
- Validate config and surface broadcast errors automatically.

Override gas price at runtime (keeps other defaults):

```go
am := cli.ActionMsg()
am.SetTxHelperConfig(&tx.TxHelperConfig{ GasPrice: "0.025ulume" })
```

Send actions (ActionMsg)

RequestAction:

```go
resp, err := cli.ActionMsg().RequesAction(
  ctx,
  "CASCADE",
  metadataJSON,              // stringified JSON
  "23800ulume",             // positive integer ulume amount
  fmt.Sprintf("%d", time.Now().Add(25*time.Hour).Unix()), // future expiry
)
```

FinalizeCascadeAction:

```go
resp, err := cli.ActionMsg().FinalizeCascadeAction(ctx, actionID, []string{"rqid-1", "rqid-2"})
```

Validation rules (built-in)

- RequestAction:
  - `actionType`, `metadata`, `price`, `expirationTime` required.
  - `price`: must be `<positive-int>ulume`.
  - `expirationTime`: future Unix seconds.
- FinalizeCascadeAction:
  - `actionId` required; `rqIdsIds` must have non-empty entries.

Notes

- Method name is currently `RequesAction` (typo kept for compatibility).
- Tx uses simulation + adjustment + padding before sign/broadcast.
