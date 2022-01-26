# flow-voter

flow voter for polynetwork.

### Build

```shell
git clone https://github.com/polynetwork/flow-voter
cd flow-voter
go build -o flow_voter main.go
```

### Run

Before running, you need feed the configuration file `config.json`.
```
{
    "PolyConfig": {
        "RestURL": "http://seed1.poly.network:20336",
        "WalletFile": "poly.node.dat"
    },
    "BoltDbPath": "db",
    "WhitelistMethods": [
        "add",
        "remove",
        "swap",
        "unlock",
        "addExtension",
        "removeExtension",
        "registerAsset",
        "onCrossTransfer"
    ],
    "FlowConfig": {
        "SideChainId": 19,
        "EventType": "A.f8d6e0586b0a20c7.CrossChainManager.CrossChain",
        "GrpcURL": [
            "192.168.2.250:3569"
        ]
    }
}
```

Now, you can start voter as follow: 

```shell
./flow_voter -conf config.json 
```

It will generate logs under `./Log` and check relayer status by view log file.