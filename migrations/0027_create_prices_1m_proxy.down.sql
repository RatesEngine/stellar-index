-- 0027 down — drop prices_1m_proxy hypertable.
--
-- Per ADR-0025 phase 1. Rolling back this migration with phases 2-3
-- already deployed would surface as missing fiat-target VWAPs on
-- the /v1/vwap, /v1/twap, /v1/changes, /v1/history surfaces. Phase
-- 1 lands first and stays empty until phase 2 is deployed; rolling
-- back at phase 1 is harmless since nothing reads or writes the
-- table yet.

BEGIN;

DROP TABLE IF EXISTS prices_1m_proxy CASCADE;

COMMIT;
