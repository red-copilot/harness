# v5 数据模型与 schema 5

> **状态：v5 目标设计（提案）。** 当前代码仍是 schema 8。本文档中的 DDL 是 P3 的实现目标。

## 1. 原则

1. **唯一真相**：`run_journal` 是规范历史；`run_state`、`run_checkpoints`、`rm_*` 都是可重建投影。
2. **单一序列化契约**：一个 `core.Record` 信封 + 每种记录类型的 payload struct。读模型使用同一 domain struct，不引入第二套 JSON 契约。
3. **原子单元**：一次语义变更 = 一个 SQLite 事务；finalize = 一个事务。
4. **per-run 链**：`hash = sha256(prev_hash + "\n" + canonical(record-without-hash))`；genesis `prev_hash = manifest_sha256`。
5. **日志非真相**：`run_logs` 独立、追加、不进链、不参与回放；它只是可查询的遥测。
6. **失败即失败**：任何 schema/触发器/Go 校验失败都 fail-closed，不产生成功历史。

## 2. 记录信封

```go
type Record struct {
    RunID         string          `json:"run_id"`
    Seq           uint64          `json:"seq"`
    Type          RecordType      `json:"type"`
    SchemaVersion int             `json:"schema_version"`
    At            time.Time       `json:"at"`
    Payload       json.RawMessage `json:"payload"`
    PrevHash      string          `json:"prev_hash"`
    Hash          string          `json:"hash"`
}
```

- `At` 持久化为 Unix nanoseconds；读出为 UTC。
- `Payload` 是该类型 payload struct 的**规范 JSON**（键序固定、关闭 HTML escaping、无尾随换行）。
- 哈希输入是去掉 `PrevHash`/`Hash` 的信封字段 + 原始 payload 字节：

```text
canonical = {"run_id":...,"seq":...,"type":...,"schema_version":...,"at":...,"payload":<raw>}
hash      = sha256(prev_hash + "\n" + canonical)
```

- `Manifest` 的 digest 使用同一规范 JSON 函数；`run_journal.seq=1` 的 `prev_hash` 等于 `manifest_sha256`。

## 3. 记录类型注册表

每种类型有当前版本与解码器；未知类型/版本直接报错。

| 类别 | 类型（示例） | 版本 | Payload |
|---|---|---|---|
| Run | `run.created` | 1 | manifest digest、agent ID、policy digest |
| Run | `run.phase` | 1 | from/to、reason、at |
| Run | `run.state` | 1 | usage、goal_candidate、non_growth_rounds、reason_failures、stagnated |
| Run | `run.goal_candidate` | 1 | reason、at |
| Run | `run.resumed` | 1 | head_seq、normalizations |
| Run | `run.interrupted` | 1 | reason、operator |
| Run | `run.finalized` | 1 | outcome、verdict_sha256、error、usage |
| Target | `target.provisioned` | 1 | binding identity、runtime digest |
| Target | `target.closed` | 1 | result |
| Reason | `reason.started` | 1 | bounded context summary |
| Reason | `reason.completed` | 1 | admitted intent ids、goal_candidate |
| Reason | `reason.rejected` | 1 | error、redacted |
| Intent | `intent.proposed` | 1 | full Intent |
| Intent | `intent.claimed` | 1 | intent id、executor id |
| Intent | `intent.concluded` | 1 | intent id、result claim id |
| Intent | `intent.abandoned` | 1 | intent id、reason |
| Attempt | `attempt.started` | 1 | full IntentAttempt |
| Attempt | `attempt.concluded` | 1 | attempt id、conclusion |
| Attempt | `attempt.failed` | 1 | attempt id、error |
| Attempt | `attempt.cancelled` | 1 | attempt id |
| Attempt | `attempt.timed_out` | 1 | attempt id、deadline |
| Evidence | `claim.created` | 1 | full Claim |
| Evidence | `observation.created` | 1 | full Observation |
| Evidence | `artifact.stored` | 1 | artifact metadata（ID=sha256） |
| Protocol | `context.truncated` | 1 | dropped counts |
| Protocol | `agent.event` | 1 | normalized tool/message event |
| Authority | `verifier.started` | 1 | evidence digest |
| Authority | `verifier.completed` | 1 | verdict digest、duration |
| Control | `control.error` | 1 | redacted error |
| Control | `budget.exceeded` | 1 | dimension、limit |

新增记录类型的规则：

1. 在 `core/record.go` 注册类型、版本、payload 类型与解码器。
2. payload 必须是可规范化的 struct，禁止 map/浮点顺序不确定字段。
3. 版本升级必须保留旧解码器；未知版本 fail closed。
4. 若记录影响 board，`core.Board.Apply` 必须有确定性处理分支。
5. 增加失败测试：未知类型、错误版本、非法 payload、回放不 panic。

## 4. Schema v5 DDL

> 所有表先创建，再创建触发器，避免触发器体引用尚未创建的表。所有表使用 `STRICT`；除明确允许 NULL 的列外均 NOT NULL。

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA synchronous = FULL;
PRAGMA busy_timeout = 5000;

CREATE TABLE schema_meta (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  version    INTEGER NOT NULL CHECK (version = 5),
  created_at INTEGER NOT NULL
) STRICT;

CREATE TABLE run_manifests (
  run_id             TEXT PRIMARY KEY,
  descriptor         BLOB NOT NULL,
  descriptor_sha256  TEXT NOT NULL CHECK (length(descriptor_sha256) = 64),
  created_at         INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE TABLE run_journal (
  run_id          TEXT NOT NULL REFERENCES run_manifests(run_id),
  seq             INTEGER NOT NULL CHECK (seq > 0),
  type            TEXT NOT NULL,
  schema_version  INTEGER NOT NULL CHECK (schema_version > 0),
  at              INTEGER NOT NULL,
  payload         BLOB NOT NULL,
  prev_hash       TEXT NOT NULL CHECK (length(prev_hash) = 64),
  hash            TEXT NOT NULL CHECK (length(hash) = 64),
  PRIMARY KEY (run_id, seq)
) STRICT, WITHOUT ROWID;

CREATE TABLE run_state (
  run_id             TEXT PRIMARY KEY REFERENCES run_manifests(run_id),
  phase              TEXT NOT NULL
                     CHECK (phase IN ('queued','provisioning','running','verifying','terminal')),
  outcome            TEXT NOT NULL DEFAULT ''
                     CHECK (outcome IN ('','succeeded','failed','cancelled','timed_out','interrupted','stagnated')),
  head_seq           INTEGER NOT NULL CHECK (head_seq > 0),
  head_hash          TEXT NOT NULL CHECK (length(head_hash) = 64),
  board_revision     INTEGER NOT NULL DEFAULT 0 CHECK (board_revision >= 0),
  usage              BLOB,
  goal_candidate     INTEGER NOT NULL DEFAULT 0 CHECK (goal_candidate IN (0,1)),
  non_growth_rounds  INTEGER NOT NULL DEFAULT 0 CHECK (non_growth_rounds >= 0),
  reason_failures    INTEGER NOT NULL DEFAULT 0 CHECK (reason_failures >= 0),
  stagnated          INTEGER NOT NULL DEFAULT 0 CHECK (stagnated IN (0,1)),
  started_at         INTEGER,
  finished_at        INTEGER,
  error              TEXT NOT NULL DEFAULT '',
  updated_at         INTEGER NOT NULL,
  FOREIGN KEY (run_id, head_seq) REFERENCES run_journal(run_id, seq),
  CHECK ((phase = 'terminal' AND outcome <> '') OR (phase <> 'terminal' AND outcome = ''))
) STRICT, WITHOUT ROWID;

CREATE TABLE run_finalization (
  run_id          TEXT PRIMARY KEY REFERENCES run_manifests(run_id),
  outcome         TEXT NOT NULL
                  CHECK (outcome IN ('succeeded','failed','cancelled','timed_out','interrupted','stagnated')),
  journal_seq     INTEGER NOT NULL CHECK (journal_seq > 0),
  verdict_sha256  TEXT CHECK (verdict_sha256 IS NULL OR length(verdict_sha256) = 64),
  finalized_at    INTEGER NOT NULL,
  FOREIGN KEY (run_id, journal_seq) REFERENCES run_journal(run_id, seq)
) STRICT, WITHOUT ROWID;

CREATE TABLE verdicts (
  run_id       TEXT PRIMARY KEY REFERENCES run_manifests(run_id),
  success      INTEGER NOT NULL CHECK (success IN (0,1)),
  score        REAL NOT NULL DEFAULT 0,
  message      TEXT NOT NULL DEFAULT '',
  evidence     BLOB,
  verifier_id  TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE TABLE run_checkpoints (
  run_id        TEXT NOT NULL,
  seq           INTEGER NOT NULL CHECK (seq > 0),
  state_version INTEGER NOT NULL CHECK (state_version > 0),
  state         BLOB NOT NULL,
  state_sha256  TEXT NOT NULL CHECK (length(state_sha256) = 64),
  head_hash     TEXT NOT NULL CHECK (length(head_hash) = 64),
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (run_id, seq),
  FOREIGN KEY (run_id, seq) REFERENCES run_journal(run_id, seq)
) STRICT, WITHOUT ROWID;

CREATE TABLE artifact_blobs (
  sha256     TEXT PRIMARY KEY CHECK (length(sha256) = 64),
  size       INTEGER NOT NULL CHECK (size >= 0),
  data       BLOB NOT NULL,
  created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE TABLE run_logs (
  run_id      TEXT NOT NULL REFERENCES run_manifests(run_id),
  seq         INTEGER NOT NULL CHECK (seq > 0),
  session_id  TEXT NOT NULL DEFAULT '',
  stream      TEXT NOT NULL,
  at          INTEGER NOT NULL,
  text        TEXT NOT NULL,
  PRIMARY KEY (run_id, seq)
) STRICT, WITHOUT ROWID;

-- 派生读模型；允许 DELETE 以支持重建。真相永远是 run_journal。
CREATE TABLE rm_claims (
  run_id  TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL,
  kind    TEXT NOT NULL, payload BLOB NOT NULL,
  PRIMARY KEY (run_id, id)
) STRICT, WITHOUT ROWID;

CREATE TABLE rm_intents (
  run_id  TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL,
  state   TEXT NOT NULL, payload BLOB NOT NULL,
  PRIMARY KEY (run_id, id)
) STRICT, WITHOUT ROWID;

CREATE TABLE rm_attempts (
  run_id     TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL,
  intent_id  TEXT NOT NULL, phase TEXT NOT NULL, payload BLOB NOT NULL,
  PRIMARY KEY (run_id, id)
) STRICT, WITHOUT ROWID;

CREATE TABLE rm_observations (
  run_id     TEXT NOT NULL, id TEXT NOT NULL, seq INTEGER NOT NULL,
  intent_id  TEXT NOT NULL, attempt_id TEXT NOT NULL, target TEXT NOT NULL,
  payload    BLOB NOT NULL,
  PRIMARY KEY (run_id, id)
) STRICT, WITHOUT ROWID;

CREATE TABLE rm_artifacts (
  run_id      TEXT NOT NULL, artifact_id TEXT NOT NULL,
  attempt_id  TEXT NOT NULL, name TEXT NOT NULL,
  sha256      TEXT NOT NULL CHECK (length(sha256) = 64),
  size        INTEGER NOT NULL, media_type TEXT NOT NULL,
  redacted    INTEGER NOT NULL DEFAULT 0 CHECK (redacted IN (0,1)),
  payload     BLOB NOT NULL,
  PRIMARY KEY (run_id, artifact_id, attempt_id, name)
) STRICT, WITHOUT ROWID;

-- 触发器：全部在表创建之后定义。
CREATE TRIGGER run_manifests_no_update BEFORE UPDATE ON run_manifests
BEGIN SELECT RAISE(ABORT, 'manifest is immutable'); END;
CREATE TRIGGER run_manifests_no_delete BEFORE DELETE ON run_manifests
BEGIN SELECT RAISE(ABORT, 'manifest is immutable'); END;

CREATE TRIGGER run_journal_no_update BEFORE UPDATE ON run_journal
BEGIN SELECT RAISE(ABORT, 'journal is append-only'); END;
CREATE TRIGGER run_journal_no_delete BEFORE DELETE ON run_journal
BEGIN SELECT RAISE(ABORT, 'journal is append-only'); END;
CREATE TRIGGER run_journal_stop_after_terminal BEFORE INSERT ON run_journal
WHEN EXISTS (SELECT 1 FROM run_state WHERE run_id = NEW.run_id AND phase = 'terminal')
BEGIN SELECT RAISE(ABORT, 'run is terminal'); END;

CREATE TRIGGER run_state_terminal_requires_finalization BEFORE UPDATE OF phase ON run_state
WHEN NEW.phase = 'terminal' AND NOT EXISTS (
  SELECT 1 FROM run_finalization f
  WHERE f.run_id = NEW.run_id AND f.outcome = NEW.outcome
)
BEGIN SELECT RAISE(ABORT, 'terminal requires matching finalization'); END;
CREATE TRIGGER run_state_terminal_immutable BEFORE UPDATE ON run_state
WHEN OLD.phase = 'terminal'
BEGIN SELECT RAISE(ABORT, 'terminal state is immutable'); END;
CREATE TRIGGER run_state_no_delete BEFORE DELETE ON run_state
BEGIN SELECT RAISE(ABORT, 'run state is not deletable'); END;

CREATE TRIGGER run_finalization_success_requires_verdict BEFORE INSERT ON run_finalization
WHEN NEW.outcome = 'succeeded' AND NOT EXISTS (
  SELECT 1 FROM verdicts v WHERE v.run_id = NEW.run_id AND v.success = 1
)
BEGIN SELECT RAISE(ABORT, 'successful finalization requires successful verdict'); END;
CREATE TRIGGER run_finalization_no_update BEFORE UPDATE ON run_finalization
BEGIN SELECT RAISE(ABORT, 'finalization is immutable'); END;
CREATE TRIGGER run_finalization_no_delete BEFORE DELETE ON run_finalization
BEGIN SELECT RAISE(ABORT, 'finalization is immutable'); END;

CREATE TRIGGER verdicts_require_verifying BEFORE INSERT ON verdicts
WHEN NOT EXISTS (
  SELECT 1 FROM run_state s WHERE s.run_id = NEW.run_id AND s.phase = 'verifying'
)
BEGIN SELECT RAISE(ABORT, 'verdict requires verifying phase'); END;
CREATE TRIGGER verdicts_no_update BEFORE UPDATE ON verdicts
BEGIN SELECT RAISE(ABORT, 'verdict is immutable'); END;
CREATE TRIGGER verdicts_no_delete BEFORE DELETE ON verdicts
BEGIN SELECT RAISE(ABORT, 'verdict is immutable'); END;

CREATE TRIGGER artifact_blobs_no_update BEFORE UPDATE ON artifact_blobs
BEGIN SELECT RAISE(ABORT, 'artifact blobs are immutable'); END;
CREATE TRIGGER artifact_blobs_no_delete BEFORE DELETE ON artifact_blobs
BEGIN SELECT RAISE(ABORT, 'artifact blobs are immutable'); END;

CREATE TRIGGER run_logs_no_update BEFORE UPDATE ON run_logs
BEGIN SELECT RAISE(ABORT, 'logs are append-only'); END;
CREATE TRIGGER run_logs_no_delete BEFORE DELETE ON run_logs
BEGIN SELECT RAISE(ABORT, 'logs are append-only'); END;
CREATE TRIGGER run_logs_stop_after_terminal BEFORE INSERT ON run_logs
WHEN EXISTS (SELECT 1 FROM run_state WHERE run_id = NEW.run_id AND phase = 'terminal')
BEGIN SELECT RAISE(ABORT, 'run is terminal'); END;

CREATE INDEX run_state_phase ON run_state(phase);
CREATE INDEX run_logs_run_seq ON run_logs(run_id, seq);
CREATE INDEX rm_claims_seq ON rm_claims(run_id, seq);
CREATE INDEX rm_intents_seq ON rm_intents(run_id, seq);
CREATE INDEX rm_attempts_seq ON rm_attempts(run_id, seq);
CREATE INDEX rm_observations_seq ON rm_observations(run_id, seq);
CREATE INDEX rm_artifacts_run ON rm_artifacts(run_id, attempt_id, name);
```

## 5. 事务配方

### 5.1 Create

```text
BEGIN IMMEDIATE
  INSERT run_manifests(run_id, canonical(manifest), sha256(canonical(manifest)), now)
  record1 = chain(prev=manifest_sha256, seq=1, type=run.created)
  INSERT run_journal(record1)
  INSERT run_state(run_id, phase=queued, head_seq=1, head_hash=record1.hash, updated_at=now)
  project(record1)
COMMIT
```

### 5.2 Append

```text
BEGIN IMMEDIATE
  state = SELECT ... FROM run_state WHERE run_id=?           -- assert phase != terminal
  assert state.head_seq == batch.ExpectedHead
  assert chain starts from state.head_hash
  for record in batch.Records: seq++, compute hash, INSERT run_journal
  UPDATE run_state SET head_seq, head_hash, phase, outcome='', usage, control flags, updated_at
  project(records)                                           -- upsert rm_*
  maybe INSERT run_checkpoints (phase change or every 256 records)
COMMIT
```

`ExpectedHead` 不匹配返回 `ErrStaleHead`；事务回滚，不留下部分记录。`batch` 必须是 `core.SealedBatch`。

### 5.3 Finalize

```text
BEGIN IMMEDIATE
  state = SELECT ... FROM run_state WHERE run_id=?
  assert state.phase != terminal
  assert state.head_seq == finalization.ExpectedHead
  assert finalization.Outcome valid
  if Outcome == succeeded:
      assert state.phase == verifying
      assert finalization.Verdict != nil && Verdict.Success
  else:
      // 允许携带成功 verdict：verifier 成功后 cleanup 失败会把 outcome 降级为 failed，
      // 但 verdict 仍是审计事实，必须保留。成功方向仍由上面的 succeeded 分支强制。

  finalRecord = chain(prev=state.head_hash, seq=state.head_seq+1, type=run.finalized)
  INSERT run_journal(finalRecord)                            -- 1
  if finalization.Verdict != nil:
      INSERT verdicts(...)                                   -- 2  trigger: phase=verifying
  INSERT run_finalization(run_id, outcome, finalRecord.seq,
                          sha256(verdict), now)              -- 3  trigger: success needs verdict
  UPDATE run_state SET phase=terminal, outcome=..., head_seq, head_hash,
                       usage, finished_at, error, updated_at -- 4  trigger: finalization exists
  INSERT run_checkpoints(seq=finalRecord.seq, ...)           -- 5
COMMIT
```

**禁止**在 `verdicts` 上要求 finalization 已存在、同时在 `run_finalization` 上要求 verdict 已存在；两者会形成循环等待，使成功终结永远无法写入。v5 的顺序是单向的：journal → verdict → finalization → state → checkpoint。

### 5.4 Interrupt

操作者中断走同一个 finalize 内部路径：

```text
BEGIN IMMEDIATE
  state = SELECT ...
  assert state.phase != terminal
  assert state.head_seq == expectedHead
  record = chain(type=run.interrupted)
  INSERT run_journal(record)
  INSERT run_finalization(outcome=interrupted, journal_seq=record.seq)
  UPDATE run_state -> terminal(interrupted)
  INSERT run_checkpoints
COMMIT
```

调用方必须先持 writer lease，且 Runner 的进程内 active-run 表不含该 ID。

### 5.5 Logs

```text
BEGIN IMMEDIATE
  assert run_state.phase != terminal
  base = SELECT COALESCE(MAX(seq),0) FROM run_logs WHERE run_id=?
  INSERT run_logs(run_id, base+i, session_id, stream, at, redacted_text)
COMMIT
```

日志不计算链、不写 journal、不参与回放。文本已由 `Redactor.SealLog` 处理。

## 6. 链与回放

### 6.1 链

- `prev_hash` 来自上一条 journal 记录；seq=1 来自 manifest digest。
- 每条记录的 hash 覆盖信封与 payload；任何字节改动都会被 `VerifyChain` 发现。
- 链是 per-run 的；不同 Run 之间没有共享头，消除 v4 全局链的跨 Run 耦合与单写者假设。

### 6.2 Checkpoint

- 内容：`core.Checkpoint{SchemaVersion, State, Board}` 的规范 JSON。
- `state_sha256 = sha256(canonical(checkpoint))`；`head_hash` 等于 seq 处 journal 记录的 hash。
- 写入时机：每次 phase 变更、每 256 条记录、finalize。
- checkpoint 是加速与一致性锚点，不是真相；校验失败时回退全量回放。

### 6.3 Replay 算法

```text
1. load + validate manifest
2. load run_state; validate phase/outcome/head
3. cp = newest checkpoint with cp.seq <= state.head_seq
4. if cp exists:
     verify sha256(canonical(cp)) == cp.state_sha256
     verify journal[cp.seq].hash == cp.head_hash
     board = cp.Board; prev = cp.head_hash; from = cp.seq+1
   else:
     board = empty; prev = manifest_sha256; from = 1
5. for seq in from..state.head_seq:
     rec = journal[seq]
     verify rec.prev_hash == prev and rec.hash == sha256(prev+"\n"+canonical(rec))
     board.Apply(rec); verify state-machine transition
     prev = rec.hash
6. verify prev == state.head_hash
7. verify final board/state against run_state (phase, revision, outcome)
8. return Restored{Manifest, State, Board, HeadSeq, HeadHash}
```

任何一步失败返回 `ErrCorruptJournal`（带 run_id/seq/原因），resume 中止；操作者可中断该 Run，但不得静默修复。

## 7. 读模型

- `project(record)` 是纯函数：把记录 upsert 到 `rm_*`。它不决定真相，只加速查询。
- `rebuild-readmodel --id` 删除该 Run 的 `rm_*` 行并按 journal 重放；结果必须与 `Reader.Board` 校验一致。
- `Reader.Board` 从 `rm_*` 构造 `core.Board` 并 `Validate`；失败时回退 journal 回放；仍失败则 fail closed。
- 读命令使用 `OpenReader`（只读、无 lease），不会与写者竞争。

## 8. Artifact 与日志

- `artifact.stored` 记录包含 `ID == sha256(content)`、attempt、name、media type、size、redacted。
- `ArtifactWriter.Put` 先写 `artifact_blobs`（独立事务），再由 journal append 写 `artifact.stored`。崩溃只可能留下不可达 blob，不会产生"有记录无内容"的证据。
- 读取时重算 SHA-256 与 size；不匹配 fail closed。
- 文本证据在 `SealArtifact` 时脱敏；二进制证据若含已知 secret 则拒绝。
- 日志独立表；finalize 前停止日志泵，terminal 后触发器拒绝写入。

## 9. 版本与兼容

- SQLite 版本固定为 5；`OpenWriter/OpenReader/OpenFresh` 拒绝 8 与未知版本，不原地迁移。
- `Manifest.SchemaVersion = 5`；未知版本拒绝。
- 记录类型各自有 `SchemaVersion`；未知类型/版本拒绝。
- 读模型与 checkpoint 都带版本；版本不匹配时按 journal 重建，不尝试猜测。
- 任何改变规范 JSON 语义的修改都必须升级对应记录版本并提供旧解码器。

## 10. 切换

见 [migration.md](migration.md) 与 [ADR 0007](adr/0007-schema-v5-cutover.md)：

1. 停止所有 v4 写者，确认 `<db>.lock` 无持有者。
2. `db backup` 生成 v8 副本与 manifest（SHA-256、大小、schema、行数、事件头、artifact 字节）。
3. 独立 `db verify-backup`，任何不匹配即失败。
4. 归档 v8 库与 `-wal`/`-shm`（不删除）。
5. 在同目录临时文件创建 v5 库，fsync，原子 rename；写 `cutover.json` 记录两侧 digest。
6. v5 拒绝 v8；`doctor` 验证 schema 5、空历史、链、lease。
7. 冒烟 Run 必须只通过 verifier 成功。
