// Copyright 2015 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package internal

import (
	"fmt"
	"github.com/amazechain/amc/common"
	"github.com/amazechain/amc/common/block"
	"github.com/amazechain/amc/common/crypto"
	"github.com/amazechain/amc/common/transaction"
	"github.com/amazechain/amc/common/types"
	"github.com/amazechain/amc/internal/consensus"
	"github.com/amazechain/amc/internal/consensus/misc"
	vm2 "github.com/amazechain/amc/internal/vm"
	"github.com/amazechain/amc/internal/vm/evmtypes"
	"github.com/amazechain/amc/modules/ethdb"
	"github.com/amazechain/amc/modules/state"
	"github.com/amazechain/amc/params"
	"github.com/ledgerwatch/erigon-lib/kv"
)

// StateProcessor is a basic Processor, which takes care of transitioning
// state from one point to another.
//
// StateProcessor implements Processor.
type StateProcessor struct {
	config *params.ChainConfig // Chain configuration options
	bc     *BlockChain         // Canonical block chain
	engine consensus.Engine    // Consensus engine used for block rewards
}

// NewStateProcessor initialises a new StateProcessor.
func NewStateProcessor(config *params.ChainConfig, bc *BlockChain, engine consensus.Engine) *StateProcessor {
	return &StateProcessor{
		config: config,
		bc:     bc,
		engine: engine,
	}
}

// Process processes the state changes according to the Ethereum rules by running
// the transaction messages using the statedb and applying any rewards to both
// the processor (coinbase) and any included uncles.
//
// Process returns the receipts and logs accumulated during the process and
// returns the amount of gas that was used in the process. If any of the
// transactions failed to execute due to insufficient gas it will return an error.
func (p *StateProcessor) Process(tx kv.RwTx, b *block.Block, ibs *state.IntraBlockState, stateReader state.StateReader, stateWriter state.WriterWithChangeSets, blockHashFunc func(n uint64) types.Hash) (block.Receipts, []*block.Log, uint64, error) {
	header := b.Header()
	usedGas := new(uint64)
	gp := new(common.GasPool)
	gp.AddGas(b.GasLimit())

	var (
		rejectedTxs []*RejectedTx
		includedTxs transaction.Transactions
		receipts    block.Receipts
	)

	chainReader := p.bc
	cfg := vm2.Config{}

	//if !cfg.ReadOnly {
	//	if err := InitializeBlockExecution(p.engine, chainReader, b.Header().(*block.Header), b.Transactions(), b.Uncles(), params.AmazeChainConfig, ibs); err != nil {
	//		return nil, nil, 0, err
	//	}
	//}
	//dpath := paths.DefaultDataDir()
	//
	//rw, err := p.bc.ChainDB.BeginRw(context.Background())
	//if nil != err {
	//	return nil, nil, 0, err
	//}
	//defer rw.Rollback()
	//
	//batch := olddb.NewHashBatch(rw, p.bc.Quit(), dpath)
	//defer batch.Rollback()
	//
	//stateReader, stateWriter, err := NewStateReaderWriter(batch, rw, b.Number64().Uint64(), true)
	//if nil != err {
	//	return nil, nil, 0, err
	//}
	//ibs := state.New(stateReader)

	chainConfig := p.config
	if chainConfig.DAOForkSupport && chainConfig.DAOForkBlock != nil && chainConfig.DAOForkBlock.Cmp(b.Number64().ToBig()) == 0 {
		misc.ApplyDAOHardFork(ibs)
	}
	noop := state.NewNoopWriter()

	//posa, isPoSA := p.engine.(*apoa.Apoa)
	for i, tx := range b.Transactions() {
		//if isPoSA {
		//	if isSystemTx, err := posa.IsSystemTransaction(tx, b.Header()); err != nil {
		//		return nil, nil, 0, err
		//	} else if isSystemTx {
		//		continue
		//	}
		//}
		ibs.Prepare(tx.Hash(), b.Hash(), i)
		receipt, _, err := ApplyTransaction(chainConfig, blockHashFunc, p.engine, nil, gp, ibs, noop, header.(*block.Header), tx, usedGas, cfg)
		if err != nil {
			if !cfg.StatelessExec {
				return nil, nil, 0, fmt.Errorf("could not apply tx %d from block %d [%v]: %w", i, b.Number64(), tx.Hash().String(), err)
			}
			rejectedTxs = append(rejectedTxs, &RejectedTx{i, err.Error()})
		} else {
			includedTxs = append(includedTxs, tx)
			if !cfg.NoReceipts {
				receipts = append(receipts, receipt)
			}
		}
	}

	if !cfg.StatelessExec && *usedGas != header.(*block.Header).GasUsed {
		return nil, nil, 0, fmt.Errorf("gas used by execution: %d, in header: %d", *usedGas, header.(*block.Header).GasUsed)
	}

	if !cfg.ReadOnly {
		txs := b.Transactions()
		if _, _, _, err := FinalizeBlockExecution(tx, p.engine, stateReader, b.Header().(*block.Header), txs, b.Uncles(), stateWriter, chainConfig, ibs, receipts, chainReader, false, p.config.IsBeijing(b.Number64().Uint64())); err != nil {
			return nil, nil, 0, err
		}
	}
	allLogs := ibs.Logs()

	//if err := ibs.CommitBlock(chainConfig.Rules(header.Number64().Uint64()), stateWriter); err != nil {
	//	return nil, nil, 0, fmt.Errorf("committing block %d failed: %w", header.Number64().Uint64(), err)
	//}
	//
	//if err := stateWriter.WriteChangeSets(); err != nil {
	//	return nil, nil, 0, fmt.Errorf("writing changesets for block %d failed: %w", header.Number64().Uint64(), err)
	//}
	//if err := rw.Commit(); nil != err {
	//	return nil, nil, 0, err
	//}

	return receipts, allLogs, *usedGas, nil
}

// applyTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. It returns the receipt
// for the transaction, gas used and an error if the transaction failed,
// indicating the block was invalid.
func applyTransaction(config *params.ChainConfig, engine consensus.Engine, gp *common.GasPool, ibs *state.IntraBlockState, stateWriter state.StateWriter, header *block.Header, tx *transaction.Transaction, usedGas *uint64, evm vm2.VMInterface, cfg vm2.Config) (*block.Receipt, []byte, error) {
	rules := evm.ChainRules()
	//msg, err := tx.AsMessage(*transaction.MakeSigner(config, header.Number.Uint64()))
	//if err != nil {
	//	return nil, nil, err
	//}

	msg, err := tx.AsMessage(transaction.MakeSigner(config, header.Number.ToBig()), header.BaseFee)
	if err != nil {
		return nil, nil, err
	}

	msg.SetCheckNonce(!cfg.StatelessExec)

	if msg.FeeCap().IsZero() && engine != nil {
		//syscall := func(contract types.Address, data []byte) ([]byte, error) {
		//	return SysCallContract(contract, data, *config, ibs, header, engine)
		//}
		msg.SetIsFree(false)
	}

	txContext := NewEVMTxContext(msg)
	if cfg.TraceJumpDest {
		h := tx.Hash()
		txContext.TxHash = h
	}

	// Update the evm with the new transaction context.
	evm.Reset(txContext, ibs)

	result, err := ApplyMessage(evm, msg, gp, true /* refunds */, false /* gasBailout */)
	if err != nil {
		return nil, nil, err
	}
	// Update the state with pending changes
	if err = ibs.FinalizeTx(rules, stateWriter); err != nil {
		return nil, nil, err
	}
	*usedGas += result.UsedGas

	// Set the receipt logs and create the bloom filter.
	// based on the eip phase, we're passing whether the root touch-delete accounts.
	var receipt *block.Receipt
	if !cfg.NoReceipts {
		// by the tx.
		receipt = &block.Receipt{Type: tx.Type(), CumulativeGasUsed: *usedGas}
		if result.Failed() {
			receipt.Status = block.ReceiptStatusFailed
		} else {
			receipt.Status = block.ReceiptStatusSuccessful
		}

		receipt.TxHash = tx.Hash()
		receipt.GasUsed = result.UsedGas
		// if the transaction created a contract, store the creation address in the receipt.
		if msg.To() == nil {
			receipt.ContractAddress = crypto.CreateAddress(evm.TxContext().Origin, tx.Nonce())
		}
		// Set the receipt logs and create a bloom for filtering
		receipt.Logs = ibs.GetLogs(tx.Hash())
		receipt.Bloom = block.CreateBloom(block.Receipts{receipt})
		receipt.BlockNumber = header.Number
		receipt.TransactionIndex = uint(ibs.TxIndex())
	}

	return receipt, result.ReturnData, err
}

// ApplyTransaction attempts to apply a transaction to the given state database
// and uses the input parameters for its environment. It returns the receipt
// for the transaction, gas used and an error if the transaction failed,
// indicating the block was invalid.
func ApplyTransaction(config *params.ChainConfig, blockHashFunc func(n uint64) types.Hash, engine consensus.Engine, author *types.Address, gp *common.GasPool, ibs *state.IntraBlockState, stateWriter state.StateWriter, header *block.Header, tx *transaction.Transaction, usedGas *uint64, cfg vm2.Config) (*block.Receipt, []byte, error) {
	// Create a new context to be used in the EVM environment

	// Add addresses to access list if applicable
	// about the transaction and calling mechanisms.
	//cfg.SkipAnalysis = SkipAnalysis(config, header.Number.Uint64())

	var vmenv vm2.VMInterface

	//if tx.IsStarkNet() {
	//	vmenv = &vm.CVMAdapter{Cvm: NewCVM(ibs)}
	//} else {
	blockContext := NewEVMBlockContext(header, blockHashFunc, engine, author)
	vmenv = vm2.NewEVM(blockContext, evmtypes.TxContext{}, ibs, config, cfg)
	//}

	return applyTransaction(config, engine, gp, ibs, stateWriter, header, tx, usedGas, vmenv, cfg)
}

func NewStateReaderWriter(
	batch ethdb.Database,
	tx kv.RwTx,
	blockNumber uint64,
	writeChangeSets bool,
	// accumulator *shards.Accumulator,
	// initialCycle bool,
	// stateStream bool,
) (state.StateReader, state.WriterWithChangeSets, error) {

	var stateReader state.StateReader
	var stateWriter state.WriterWithChangeSets

	stateReader = state.NewPlainStateReader(tx)
	if writeChangeSets {
		stateWriter = state.NewPlainStateWriter(batch, tx, blockNumber)
	} else {
		stateWriter = state.NewPlainStateWriterNoHistory(batch)
	}
	return stateReader, stateWriter, nil
}
