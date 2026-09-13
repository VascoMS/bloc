-- BLOC one-month liquidation export. Debt and collateral are kept as separate sides.
WITH liquidation_events AS (
    SELECT 'debt_repaid' AS liquidation_side, b.blockchain, b.project, b.version,
           b.block_time, b.block_number, b.tx_hash, b.evt_index, b.liquidator,
           b.borrower, CAST(NULL AS VARBINARY) AS depositor, b.on_behalf_of,
           b.repayer, CAST(NULL AS VARBINARY) AS withdrawn_to, b.token_address,
           b.symbol, b.amount, b.amount_raw, b.amount_usd,
           b.project_contract_address
    FROM lending.borrow b
    WHERE b.blockchain = 'ethereum'
      AND b.transaction_type = 'liquidation'
      AND b.block_month >= DATE '__START_DATE__'
      AND b.block_month < DATE '__END_DATE__'
      AND b.block_time >= TIMESTAMP '__START_DATE__'
      AND b.block_time < TIMESTAMP '__END_DATE__'
    UNION ALL
    SELECT 'collateral_seized' AS liquidation_side, s.blockchain, s.project,
           s.version, s.block_time, s.block_number, s.tx_hash, s.evt_index,
           s.liquidator, CAST(NULL AS VARBINARY) AS borrower, s.depositor,
           s.on_behalf_of, CAST(NULL AS VARBINARY) AS repayer, s.withdrawn_to,
           s.token_address, s.symbol, s.amount, s.amount_raw, s.amount_usd,
           s.project_contract_address
    FROM lending.supply s
    WHERE s.blockchain = 'ethereum'
      AND s.transaction_type = 'liquidation'
      AND s.block_month >= DATE '__START_DATE__'
      AND s.block_month < DATE '__END_DATE__'
      AND s.block_time >= TIMESTAMP '__START_DATE__'
      AND s.block_time < TIMESTAMP '__END_DATE__'
)
SELECT l.liquidation_side, l.blockchain, l.project, l.version, l.block_time,
       l.block_number, tx.block_hash, l.tx_hash, tx.index AS tx_index,
       tx."from" AS tx_from, tx."to" AS tx_to, tx.success AS tx_success,
       tx.gas_used, tx.priority_fee_per_gas, tx.value AS tx_value,
       l.liquidator, l.borrower, l.depositor, l.on_behalf_of, l.repayer,
       l.withdrawn_to, l.token_address, l.symbol, l.amount, l.amount_raw,
       l.amount_usd, l.project_contract_address, l.evt_index
FROM liquidation_events l
LEFT JOIN ethereum.transactions tx
        ON tx.block_time = l.block_time AND tx.hash = l.tx_hash
       AND tx.block_date >= DATE '__START_DATE__'
       AND tx.block_date < DATE '__END_DATE__'
ORDER BY l.block_number, tx.index, l.evt_index, l.liquidation_side
