# L.S.D: Enterprise Distributed Architecture Blueprint

## System Overview
The L.S.D (Large Search of Data) platform has evolved from a standalone PostgreSQL/ClickHouse backend to a Petabyte-scale, zero-cloud on-premise Hadoop ecosystem. This expansion facilitates the ingestion, transformation, and high-speed search of millions of records and dynamic intelligence schemas.

## Data Flow Lifecycle
The architecture separates concerns into Transactional (Online), Batch/Stream Ingestion, Distributed Persistence, Analytical Processing (OLAP), and High-Speed Real-time Search.

**PostgreSQL (Hot/Transactional) -> Sqoop/Flume -> HDFS -> Hive/Pig (Processing) -> ClickHouse (Real-time Search Index)**

1.  **Online Transactional Processing (OLTP):**
    *   **PostgreSQL** acts as the primary source of truth for the `entities`, `entity_identity_anchors`, and relationship tagging.
    *   It handles all live mutative operations, ensuring strict constraints (like Govt ID hashing for deduplication).
    *   **Drop & Sync (Go):** Detects schema migrations dynamically via `fsnotify` without server reboots, managing new dataset integrations seamlessly.

2.  **High-Volume Ingestion Layer:**
    *   **Sqoop (Batch):** Deployed for moving terabytes of structured relational data from PostgreSQL schemas to HDFS. Parallel mappers ensure rapid data offloading.
    *   **Flume (Real-Time Streams):** Captures high-velocity API access logs, syslog audit trails, and server events, routing them through durable channels and writing directly into time-partitioned HDFS directories.

3.  **Distributed Storage Layer:**
    *   **HDFS (NameNode/DataNode):** The central petabyte-scale data lake. Data is written primarily in Parquet format with Snappy compression to optimize for analytical reads.
    *   Directory structures are partitioned logically (e.g., `/lsd/raw/entities`, `/lsd/logs/api/%Y/%m/%d/%H`).

4.  **ETL & Transformation Layer (ETL/OLAP):**
    *   **Hive (Intelligence Warehousing):** Defines the OLAP schema (`lsd_warehouse`). It uses external tables to map Sqoop's raw ingestions and managed ORC tables for executing heavy aggregations (e.g., Risk Analytics per Country, Entity Intelligence Cubes).
    *   **Pig Latin:** Scripted for heavy, multi-stage data flow operations that are cumbersome in SQL.
        *   Extracts distinct entity relationships and social network risk propagation.
        *   Parses financial flow analyses (net credit/debit ratios) for risk scoring.
    *   **MapReduce:** Operates on billions of text records using a custom Java implementation (`InvertedIndexJob`). It builds highly optimized, tokenized inverted indices.

5.  **Sub-Second Real-Time Search Engine:**
    *   **ClickHouse (Vectorized Analytics Engine):** Loaded with the outputs from the MapReduce inverted indices and active tables.
    *   **Omni-Search Handler (Go):** The API exposes a 360-degree entity profile search. The Go backend executes a rapid ClickHouse bitmap lookup to isolate primary `entity_id`s, then fans out to PostgreSQL to enrich the result with deep relational context (Bank Accounts, Govt IDs, Social Profiles, Property).
    *   **Multi-Filter Operations:** Go layer supports advanced UI requirements, blending Full-Text queries with `ANY`/`ALL` category matching.

## Component Specifications & Tooling

### Hadoop Ecosystem Specs
| Component | Function | Format / Ext |
| :--- | :--- | :--- |
| **Sqoop 1.4.7** | PostgreSQL to HDFS batch migrations (16 parallel mappers). | Parquet + Snappy |
| **Flume 1.9+** | Real-time ingest for API JSON logs & syslog audit events. | `.json.gz` / `.log.gz` |
| **Hive 3.x** | Distributed OLAP Data Warehouse (`wh_entities` cube). | ORC + Snappy |
| **Pig 0.17+** | Dataflow ETL (Network Risk propagation, Duplication detection). | CSV / Text |
| **MapReduce** | Java `InvertedIndexJob` with custom Partitioner and Combiner. | TSV (`token \t ids \t freq`) |

### Core Application Stack
| Component | Technology | Description |
| :--- | :--- | :--- |
| **Backend API** | Go 1.24+ | Handles REST connections, Omni-Search, & the dynamic Drop-and-Sync worker. |
| **Relational DB** | PostgreSQL 15+ | Master records, migrations, and Govt ID verification logic. |
| **Search Engine** | ClickHouse | In-memory token bitmap layers for multi-million record omni-search filtering. |

## Deployment & Execution Flow
### Step 1: SQL Schema Setup
*   `001_entity_identity_anchors.sql` -> `002_entity_extended_tables.sql` -> `003_multi_filter_tagging.sql`
*   Placed in `/databases/incoming`, picked up autonomously by the Go `DropSyncWatcher`.

### Step 2: HDFS Ingestion
*   Run `./hadoop/sqoop-jobs.sh all` to populate the `raw_entities`, `raw_bank_accounts`, `raw_social_accounts`, etc., directories in HDFS.
*   Start Flume via `flume-ng agent --conf-file hadoop/flume-lsd.conf` to push logs.

### Step 3: Transformation
*   Execute `./hadoop/pig-etl.pig` to resolve identities, build risk networks, and construct the raw MapReduce text tokens.
*   Run Java MapReduce inverted indexer: `hadoop jar target/lsd-mapreduce-1.0.jar lsd.mapreduce.InvertedIndexJob`.

### Step 4: Analytical Reporting & Real-Time Querying
*   Hive scripts initialize the warehouse schemas parsing the raw Sqoop Parquet files.
*   The Go backend API routes high-speed `OmniSearch` lookup queries, validating and returning composite UI payloads spanning the data systems.
