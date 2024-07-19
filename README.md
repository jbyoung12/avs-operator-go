<h1>AVS Operator in Go</h1>

To run the operator, run this command: 

```
go run main.go
```

Note: Currently there is a probelm with the event subscription. It is getting this error:

```
Error monitoring new tasks: failed to watch for new tasks created: notifications not supported
```

The `bindings` are generated using `abigen`. After install `abigen` as part of the `go-ethereum` devtools, you can generate the bindings for one of the ABIs using the following command:

```
abigen --abi abis/delegation.json --pkg bindings --type Delegation --out bindings/delegation.go
```

