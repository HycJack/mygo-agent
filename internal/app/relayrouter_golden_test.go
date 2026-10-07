package app

// The routing golden set (spec/relay-router.md): scripted scenarios
// with expected outcomes, run against a real coordinator. A prompt
// edit moves routing behavior silently — this is the check that
// catches it. Gated behind MYGO_GOLDEN_BASEURL (optional
// MYGO_GOLDEN_MODEL / MYGO_GOLDEN_KEY) because it burns real calls.
//
// Assertions are properties, not exact names: real models are
// probabilistic, and a golden set that demands one answer fails on
// schedule. Each case accepts a set of next names — or asserts done —
// and the fair-turns case asserts a negative (an already-spoken member
// must not be picked while others wait).

import (
	"os"
	"testing"
)

const goldenRosterDesc = "needs analysis and acceptance criteria"

func goldenSnapshot(baseURL, request, digest string, spoken []string) panelSnapshot {
	roster := []relayMember{
		{Name: "需求", Desc: goldenRosterDesc},
		{Name: "架构", Desc: "system design, trade-offs, interfaces"},
		{Name: "开发", Desc: "implementation, fixes, refactors"},
		{Name: "测试", Desc: "test plans, edge cases, quality gates"},
		{Name: "发布", Desc: "CI/CD, deploys, rollbacks"},
		{Name: "运营", Desc: "metrics, incidents, user feedback"},
	}
	return panelSnapshot{
		threadID: "golden",
		roster:   roster,
		digest:   digest,
		request:  request,
		spoken:   spoken,
		rounds:   len(spoken),
		model:    os.Getenv("MYGO_GOLDEN_MODEL"),
		baseURL:  baseURL,
		apiKey:   os.Getenv("MYGO_GOLDEN_KEY"),
	}
}

func TestGoldenRouting(t *testing.T) {
	base := os.Getenv("MYGO_GOLDEN_BASEURL")
	if base == "" {
		t.Skip("set MYGO_GOLDEN_BASEURL (plus MYGO_GOLDEN_MODEL, MYGO_GOLDEN_KEY) to run the golden set against a real coordinator")
	}
	cases := []struct {
		name    string
		request string
		digest  string
		spoken  []string
		accept  []string // accepted next names; empty means done is expected
	}{
		{
			name:    "resolved work is done",
			request: "给 AI 绘图加一个导出功能",
			digest: `User: 给 AI 绘图加一个导出功能
需求: 验收标准已确认：导出 PNG 与 SVG，文件名带时间戳。
架构: 方案已定，导出器走渲染管线复用现有 canvas，无需新依赖。
开发: 已实现并自测通过，测试全绿。
测试: 回归用例 12 条全部通过，无阻塞缺陷。
发布: 已进灰度，回滚预案就绪。
运营: 上线后首日指标正常，无相关告警。
产品流水线: 总结——导出功能已按验收标准交付并稳定运行，无遗留事项。`,
			spoken: []string{"需求", "架构", "开发", "测试", "发布", "运营"},
			accept: nil,
		},
		{
			name:    "requirements hand to a builder",
			request: "给 AI 绘图加一个导出功能",
			digest: `User: 给 AI 绘图加一个导出功能
需求: 需求清单与验收标准已明确：导出 PNG/SVG、保留图层顺序、文件名带时间戳。范围不含 PDF。`,
			spoken: []string{"需求"},
			accept: []string{"架构", "开发"},
		},
		{
			name:    "an open choice goes to a decider",
			request: "修复 AI 绘图的超时问题",
			digest: `User: 修复 AI 绘图的超时问题
开发: 超时有两种修法——A 全局调大超时到 60s，实现最小；B 按请求类型分级超时，更稳但改动大。用 A 还是 B，你拍板。`,
			spoken: []string{"开发"},
			accept: []string{"架构", "需求"},
		},
		{
			name:    "fair turns: nobody repeats while others wait",
			request: "为项目制定发布计划",
			digest: `User: 为项目制定发布计划
需求: 发布范围已圈定：导出功能 + 超时修复。
架构: 发布顺序无依赖冲突，可以直接灰度。
开发: 两个改动都已合并到主干。`,
			spoken: []string{"需求", "架构", "开发"},
			accept: []string{"测试", "发布", "运营", ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			route, err := routeDecision(t.Context(), goldenSnapshot(base, tc.request, tc.digest, tc.spoken))
			if err != nil {
				t.Fatalf("routing failed: %v", err)
			}
			t.Logf("next=%q reason=%q", route.Next, route.Reason)
			if len(tc.accept) == 0 {
				if route.Next != "" {
					t.Fatalf("next = %q, want done", route.Next)
				}
				return
			}
			matched := false
			for _, a := range tc.accept {
				if route.Next == a {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("next = %q, want one of %v (reason: %s)", route.Next, tc.accept, route.Reason)
			}
		})
	}
}
