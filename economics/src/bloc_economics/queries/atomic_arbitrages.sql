-- BLOC one-month atomic-arbitrage label export. Rows are trade legs, not profit records.
SELECT a.blockchain, a.project, a.version, a.block_time, a.block_number,
       tx.block_hash, a.tx_hash, tx.index AS tx_index,
       tx."from" AS tx_from, tx."to" AS tx_to,
       tx.success AS tx_success, tx.gas_used, tx.priority_fee_per_gas,
       tx.value AS tx_value, a.maker, a.taker, a.project_contract_address,
       a.token_sold_address, a.token_bought_address, a.token_sold_symbol,
       a.token_bought_symbol, a.token_pair, a.token_sold_amount_raw,
       a.token_bought_amount_raw, a.token_sold_amount, a.token_bought_amount,
       a.amount_usd, a.evt_index
FROM dex.atomic_arbitrages a
LEFT JOIN ethereum.transactions tx
        ON tx.block_time = a.block_time AND tx.hash = a.tx_hash
       AND tx.block_date >= DATE '__START_DATE__'
       AND tx.block_date < DATE '__END_DATE__'
WHERE a.blockchain = 'ethereum'
  AND a.block_month >= DATE '__START_DATE__'
  AND a.block_month < DATE '__END_DATE__'
  AND a.block_time >= TIMESTAMP '__START_DATE__'
  AND a.block_time < TIMESTAMP '__END_DATE__'
ORDER BY a.block_number, a.tx_index, a.evt_index
