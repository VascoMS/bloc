-- BLOC one-month sandwich label export. Dune amount_usd is trade volume, not profit.
WITH labelled AS (
    SELECT 'outer' AS leg_role, s.*
    FROM dex.sandwiches s
    WHERE s.blockchain = 'ethereum'
      AND s.block_month >= DATE '__START_DATE__'
      AND s.block_month < DATE '__END_DATE__'
      AND s.block_time >= TIMESTAMP '__START_DATE__'
      AND s.block_time < TIMESTAMP '__END_DATE__'
    UNION ALL
    SELECT 'victim' AS leg_role, s.*
    FROM dex.sandwiched s
    WHERE s.blockchain = 'ethereum'
      AND s.block_month >= DATE '__START_DATE__'
      AND s.block_month < DATE '__END_DATE__'
      AND s.block_time >= TIMESTAMP '__START_DATE__'
      AND s.block_time < TIMESTAMP '__END_DATE__'
)
SELECT l.leg_role, l.blockchain, l.project, l.version, l.block_time,
       l.block_number, tx.block_hash, l.tx_hash, tx.index AS tx_index,
       tx."from" AS tx_from, tx."to" AS tx_to,
       tx.success AS tx_success, tx.gas_used, tx.priority_fee_per_gas,
       tx.value AS tx_value, l.maker, l.taker, l.project_contract_address,
       l.token_sold_address, l.token_bought_address, l.token_sold_symbol,
       l.token_bought_symbol, l.token_pair, l.token_sold_amount_raw,
       l.token_bought_amount_raw, l.token_sold_amount, l.token_bought_amount,
       l.amount_usd, l.evt_index
FROM labelled l
LEFT JOIN ethereum.transactions tx
        ON tx.block_time = l.block_time AND tx.hash = l.tx_hash
       AND tx.block_date >= DATE '__START_DATE__'
       AND tx.block_date < DATE '__END_DATE__'
ORDER BY l.block_number, l.tx_index, l.evt_index, l.leg_role
