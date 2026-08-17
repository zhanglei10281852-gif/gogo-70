# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

本地仓库校验放过了被改过的作业台账。往一个干净 store 里提交两条事件（records=2、ledger_entries=2、valid=true），然后只把 ledger.jsonl 第一条的 payload_sha256 换成另一串 64 位十六进制，audit.jsonl 一个字节都没动，重新校验：valid 还是 true、problems 是空的，只有 notes 里多出一条 record_hash_mismatch，detail 写着 “ledger payload 1111... does not match audit payload 7157...”。我们交班卡口和 cablemend report 的退出码都只看 valid，于是这种台账被当成通过。同一个 store 如果改的是 audit.jsonl（比如把 subject 从 SYS-T 改成 SYS-X），valid 立刻变 false 并给出 problems；把 ledger 多写一条、让条数超过 audit 记录时也会 false。也就是说只有台账内容与链上 payload 对不上的这一类被降级成了提示。先不要修改代码。请调查为什么台账与哈希链的交叉校验失败不会影响 valid 结论、而篡改 audit 链却会，给出可核验证据、完整因果链，并定位具体 Go 文件和符号。

## 含 Bug 版本

- 仓库：zhanglei10281852-gif/gogo-70
- 仓库地址：https://github.com/zhanglei10281852-gif/gogo-70.git
- parent SHA：4aca214231dd88765b7cb59ada6f2682985f1271

## 复现步骤

```bash
git clone -- https://github.com/zhanglei10281852-gif/gogo-70.git bug-repro
cd bug-repro
git checkout --detach 4aca214231dd88765b7cb59ada6f2682985f1271
go test ./internal/store -run "^TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain$" -count=1 -v
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/store -run "^TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain$" -count=1 -v
=== RUN   TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain
    ledger_crosscheck_regression_test.go:66: expected the store verdict to reject the altered ledger: {Records:2 LedgerEntries:2 Valid:true HeadHash:7c65bfa5bf5ba620950ec62e6ad0bd99df387e3985e8f83cee31dd661ba8c260 Problems:[] Notes:[{Seq:1 Kind:record_hash_mismatch Detail:ledger payload 1111111111111111111111111111111111111111111111111111111111111111 does not match audit payload 7157c1d44ab5e5ae8e822fd9c19ab92af9a953fd455568aaf77d58e9df9d7fba}]}
--- FAIL: TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain (0.00s)
FAIL
FAIL	CableMend/internal/store	0.005s
FAIL

```

stderr：

```text
warning: internal/store/ledger_crosscheck_regression_test.go has type 100755, expected 100644
warning: internal/store/ledger_crosscheck_regression_test.go has type 100755, expected 100644

```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/store -run "^TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain$" -count=1 -v
=== RUN   TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain
    ledger_crosscheck_regression_test.go:66: expected the store verdict to reject the altered ledger: {Records:2 LedgerEntries:2 Valid:true HeadHash:7c65bfa5bf5ba620950ec62e6ad0bd99df387e3985e8f83cee31dd661ba8c260 Problems:[] Notes:[{Seq:1 Kind:record_hash_mismatch Detail:ledger payload 1111111111111111111111111111111111111111111111111111111111111111 does not match audit payload 7157c1d44ab5e5ae8e822fd9c19ab92af9a953fd455568aaf77d58e9df9d7fba}]}
--- FAIL: TestVerifyAuditRejectsALedgerThatDisagreesWithTheChain (0.07s)
FAIL
FAIL	CableMend/internal/store	0.198s
FAIL

```

stderr：

```text
warning: internal/store/ledger_crosscheck_regression_test.go has type 100755, expected 100644
warning: internal/store/ledger_crosscheck_regression_test.go has type 100755, expected 100644

```

## 通过条件

定位 internal/store/audit.go 的 VerifyAudit，指出 ledger[i].PayloadHash 与 records[i].PayloadHash 的逐条交叉校验把 ChainProblem 追加到了 Notes 而不是 Problems，并结合 out.Valid = len(out.Problems) == 0 与 Notes 的既定语义（只承载不影响完整性的观察，如 ProblemTimeReversed 的输入派生时间回退）解释结论为何不受影响；说明 Commit 先写 ledger.jsonl 再用同一条 entry 的 payload 哈希生成 audit 记录、因此两侧必须逐条一致，以及为什么篡改 ledger 的 payload_sha256 不影响 audit 记录的 canonical 预像与哈希重算、prev 链和 seq 校验，使这条交叉校验成为唯一能发现该篡改的检查；同时解释改 audit.jsonl 会命中 record_hash_mismatch、ledger 条数超出会命中 ledger_entry_without_audit_record，从而形成“改 audit 会红、改 ledger 不会红”的现象差异；有可核验证据且目标仓库零改动；本题的校准与远端复跑均在 golang:1.22 linux/amd64 单架构完成，结论可用同一环境复核。
