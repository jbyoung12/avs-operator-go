package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/jbyoung12/avsoperator/bindings"
	"github.com/joho/godotenv"
)

type Operator struct {
	delegation      *bindings.Delegation
	avsDirectory    *bindings.AvsDirectoryCaller
	registry        *bindings.Registry
	contract        *bindings.Contract
	privateKey      *ecdsa.PrivateKey
	client          *ethclient.Client
	chainID         *big.Int
	contractAddress common.Address
	registered      bool
}

type Config struct {
	rpcURL               string
	privateKey           string
	delegationAddress    common.Address
	contractAddress      common.Address
	stakeRegistryAddress common.Address
	avsDirectoryAddress  common.Address
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatalf("Error loading .env file: %v", err)
	}

	cfg := Config{
		rpcURL:               os.Getenv("RPC_URL"),
		privateKey:           os.Getenv("PRIVATE_KEY"),
		delegationAddress:    common.HexToAddress(os.Getenv("DELEGATION_MANAGER_ADDRESS")),
		contractAddress:      common.HexToAddress(os.Getenv("CONTRACT_ADDRESS")),
		stakeRegistryAddress: common.HexToAddress(os.Getenv("STAKE_REGISTRY_ADDRESS")),
		avsDirectoryAddress:  common.HexToAddress(os.Getenv("AVS_DIRECTORY_ADDRESS")),
	}

	ctx := context.Background()

	operator, err := NewOperator(ctx, cfg)
	if err != nil {
		log.Fatalf("Error creating operator: %v", err)
	}

	err = operator.RegisterOperator()
	if err != nil {
		log.Fatalf("Failed to register operator: %v", err)
	}

	err = operator.MonitorNewTasks()
	if err != nil {
		log.Fatalf("Error monitoring new tasks: %v", err)
	}
}

func NewOperator(ctx context.Context, cfg Config) (*Operator, error) {
	client, err := ethclient.Dial(cfg.rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ethereum clinet: %v", err)
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain id: %v", err)
	}

	delegation, err := bindings.NewDelegation(cfg.delegationAddress, client)
	if err != nil {
		return nil, fmt.Errorf("error using ABI bindings")
	}
	registry, err := bindings.NewRegistry(cfg.stakeRegistryAddress, client)
	if err != nil {
		return nil, fmt.Errorf("error using ABI bindings")
	}
	avsDirectory, err := bindings.NewAvsDirectoryCaller(cfg.avsDirectoryAddress, client)
	if err != nil {
		return nil, fmt.Errorf("error using ABI bindings")
	}
	contract, err := bindings.NewContract(cfg.contractAddress, client)
	if err != nil {
		return nil, fmt.Errorf("error using ABI bindings")
	}

	privateKeyECDSA, err := crypto.HexToECDSA(cfg.privateKey)
	if err != nil {
		log.Fatalf("Failed to load private key: %v", err)
	}

	return &Operator{
		delegation:      delegation,
		registry:        registry,
		avsDirectory:    avsDirectory,
		contract:        contract,
		privateKey:      privateKeyECDSA,
		client:          client,
		chainID:         chainID,
		contractAddress: cfg.contractAddress,
	}, nil
}

func (o *Operator) SignAndRespondToTask(taskIndex uint32, task bindings.IHelloWorldServiceManagerTask) error {
	message := fmt.Sprintf("Hello, %s", task.Name)

	messageHash := crypto.Keccak256Hash([]byte(message))
	signature, err := crypto.Sign(messageHash.Bytes(), o.privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign message: %v", err)
	}

	fmt.Printf("Signing and responding to task %d\n", taskIndex)

	transactOps, err := bind.NewKeyedTransactorWithChainID(o.privateKey, o.chainID)
	if err != nil {
		return fmt.Errorf("failed to get trasact ops: %v", err)
	}

	tx, err := o.contract.RespondToTask(transactOps,
		bindings.IHelloWorldServiceManagerTask{
			Name:             task.Name,
			TaskCreatedBlock: task.TaskCreatedBlock,
		},
		taskIndex,
		signature,
	)
	if err != nil {
		return fmt.Errorf("failed to respond to task: %v", err)
	}

	receipt, err := bind.WaitMined(context.Background(), o.client, tx)
	if err != nil {
		return fmt.Errorf("failed to wait for transaction to be mined: %v", err)
	}
	if receipt.Status == types.ReceiptStatusFailed {
		return fmt.Errorf("receipt status failed")
	}

	fmt.Printf("Successfully responded to task %d\n", taskIndex)
	return nil
}

func (o *Operator) RegisterOperator() error {
	log.Println("check")

	transactOps, err := bind.NewKeyedTransactorWithChainID(o.privateKey, o.chainID)
	if err != nil {
		return fmt.Errorf("failed to get trasact ops: %v", err)
	}

	fromAddress := crypto.PubkeyToAddress(o.privateKey.PublicKey)

	tx, err := o.delegation.RegisterAsOperator(transactOps,
		bindings.IDelegationManagerOperatorDetails{
			EarningsReceiver:         fromAddress,
			DelegationApprover:       common.HexToAddress("0x0000000000000000000000000000000000000000"),
			StakerOptOutWindowBlocks: 0,
		}, "")
	if err != nil {
		return fmt.Errorf("failed to register as operator: %v", err)
	}

	o.registered = true

	receipt, err := bind.WaitMined(context.Background(), o.client, tx)
	if err != nil {
		return fmt.Errorf("failed to wait for transaction to be mined: %v", err)
	}
	if receipt.Status == types.ReceiptStatusFailed {
		return fmt.Errorf("receipt status failed")
	}

	log.Println("Operator registered on EL successfully")

	// generate salt and expiry in 1 hour
	salt := make([]byte, 32)
	_, err = rand.Read(salt) // reads random bytes into salt
	if err != nil {
		panic(err)
	}
	var saltBytes [32]byte
	copy(saltBytes[:], salt)

	expiry := big.NewInt(time.Now().Add(time.Hour).Unix())

	digestHash, err := o.avsDirectory.CalculateOperatorAVSRegistrationDigestHash(nil, fromAddress, o.contractAddress, saltBytes, expiry)
	if err != nil {
		return fmt.Errorf("failed to calculate digest hash: %v", err)
	}

	signature, err := crypto.Sign(digestHash[:], o.privateKey)
	if err != nil {
		return fmt.Errorf("failed to sign digest hash: %v", err)
	}

	signature[64] += 27 // Adjust according to Ethereum's v value requirements

	tx2, err := o.registry.RegisterOperatorWithSignature(transactOps, fromAddress,
		bindings.ISignatureUtilsSignatureWithSaltAndExpiry{
			Expiry:    expiry,
			Salt:      saltBytes,
			Signature: signature,
		})
	if err != nil {
		return fmt.Errorf("failed to register operator with signature: %v", err)
	}

	receipt, err = bind.WaitMined(context.Background(), o.client, tx2)
	if err != nil {
		return fmt.Errorf("failed to wait for transaction to be mined: %v", err)
	}
	if receipt.Status == types.ReceiptStatusFailed {
		return fmt.Errorf("receipt status failed")
	}

	fmt.Println("Operator registered on AVS successfully")
	return nil
}

func (o *Operator) MonitorNewTasks() error {
	newTaskCreated := make(chan *bindings.ContractNewTaskCreated)
	defer close(newTaskCreated)

	subs, err := o.contract.WatchNewTaskCreated(&bind.WatchOpts{}, newTaskCreated, []uint32{})
	if err != nil {
		return fmt.Errorf("failed to watch for new tasks created: %v", err)
	}
	defer subs.Unsubscribe()

	log.Println("Monitoring for new tasks..")
	for {
		select {
		case event := <-newTaskCreated:
			log.Printf("New task detected: Hello, %v\n", event.Task.Name)
			err := o.SignAndRespondToTask(event.TaskIndex, event.Task)
			if err != nil {
				return fmt.Errorf("fould not sign and respond to task: %v", err)
			}
		case err := <-subs.Err():
			return fmt.Errorf("failed new task created subscription: %v", err)
		}
	}
}
