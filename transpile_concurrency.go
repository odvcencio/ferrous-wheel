package ferrouswheel

import (
	"fmt"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

func (t *fwTranspiler) selectorPipelineRewrite(n *gotreesitter.Node) (string, bool) {
	operand := t.childByField(n, "operand")
	field := t.childByField(n, "field")
	if operand == nil || field == nil || t.nodeType(operand) != "pipeline_expression" {
		return "", false
	}

	pipeLeft := t.childByField(operand, "left")
	pipeRight := t.childByField(operand, "right")
	if pipeLeft == nil || pipeRight == nil {
		return "", false
	}

	return fmt.Sprintf("%s.%s(%s)", t.emit(pipeRight), t.text(field), t.emit(pipeLeft)), true
}

func (t *fwTranspiler) concurrentStatementList(n *gotreesitter.Node) *gotreesitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if t.nodeType(child) != "block" {
			continue
		}
		for j := 0; j < int(child.NamedChildCount()); j++ {
			stmtList := child.NamedChild(j)
			if t.nodeType(stmtList) == "statement_list" {
				return stmtList
			}
		}
		return child
	}
	return nil
}

func (t *fwTranspiler) throttleSiteID(n *gotreesitter.Node) string {
	return fmt.Sprintf("throttle_%d", n.StartByte())
}

// select! { arm, arm, ... } -> Go select statement
func (t *fwTranspiler) emitSelectBlock(n *gotreesitter.Node) string {
	var b strings.Builder
	b.WriteString("select {\n")

	for i := 0; i < int(n.NamedChildCount()); i++ {
		arm := n.NamedChild(i)
		if t.nodeType(arm) != "select_arm" {
			continue
		}

		varNode := t.childByField(arm, "var")
		chanNode := t.childByField(arm, "chan")
		durNode := t.childByField(arm, "duration")
		bodyNode := t.childByField(arm, "body")
		if bodyNode == nil {
			continue
		}

		switch {
		case varNode != nil && chanNode != nil:
			fmt.Fprintf(&b, "case %s := <-%s:\n\t%s\n",
				t.text(varNode), t.emit(chanNode), t.emit(bodyNode))
		case durNode != nil:
			t.needsTime = true
			fmt.Fprintf(&b, "case <-time.After(%s):\n\t%s\n",
				t.emit(durNode), t.emit(bodyNode))
		default:
			fmt.Fprintf(&b, "default:\n\t%s\n", t.emit(bodyNode))
		}
	}

	b.WriteString("}")
	return b.String()
}

// fan out workers, 10 { body } -> WaitGroup + goroutines
func (t *fwTranspiler) emitFanOut(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	countNode := t.childByField(n, "count")
	if nameNode == nil || countNode == nil {
		return t.text(n)
	}
	t.needsSync = true

	name := t.text(nameNode)
	block := t.findBlock(n)

	var b strings.Builder
	fmt.Fprintf(&b, "var _wg_%s sync.WaitGroup\n", name)
	fmt.Fprintf(&b, "for _wi := 0; _wi < %s; _wi++ {\n", t.emit(countNode))
	fmt.Fprintf(&b, "\t_wg_%s.Add(1)\n", name)
	b.WriteString("\tgo func() {\n")
	fmt.Fprintf(&b, "\t\tdefer _wg_%s.Done()\n", name)
	fmt.Fprintf(&b, "\t\t%s\n", block)
	b.WriteString("\t}()\n")
	b.WriteString("}\n")
	fmt.Fprintf(&b, "_wg_%s.Wait()", name)
	return b.String()
}

// fan in [ch1, ch2, ch3] -> helper-backed merge
func (t *fwTranspiler) emitFanIn(n *gotreesitter.Node) string {
	t.needsSync = true
	t.needsFanIn = true
	t.needsReflect = true

	var channels []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if t.nodeType(child) == "comment" {
			continue
		}
		channels = append(channels, t.emit(child))
	}
	if len(channels) == 0 {
		return t.text(n)
	}

	return fmt.Sprintf("_fwFanIn(%s)", strings.Join(channels, ", "))
}

// left |> right -> right(left)
func (t *fwTranspiler) emitPipeline(n *gotreesitter.Node) string {
	left := t.childByField(n, "left")
	right := t.childByField(n, "right")
	if left == nil || right == nil {
		return t.text(n)
	}
	return fmt.Sprintf("%s(%s)", t.emit(right), t.emit(left))
}

func (t *fwTranspiler) emitSelectorExpression(n *gotreesitter.Node) string {
	if rewritten, ok := t.selectorPipelineRewrite(n); ok {
		return rewritten
	}
	return t.emitDefault(n)
}

// concurrent { stmt1; stmt2 } -> WaitGroup wrapping each statement
func (t *fwTranspiler) emitConcurrent(n *gotreesitter.Node) string {
	t.needsSync = true

	stmtListNode := t.concurrentStatementList(n)
	if stmtListNode == nil {
		return t.text(n)
	}

	var stmts []string
	for i := 0; i < int(stmtListNode.NamedChildCount()); i++ {
		stmts = append(stmts, t.emit(stmtListNode.NamedChild(i)))
	}
	if len(stmts) == 0 {
		return "// concurrent: empty block"
	}

	var b strings.Builder
	b.WriteString("var _wg sync.WaitGroup\n")
	fmt.Fprintf(&b, "_wg.Add(%d)\n", len(stmts))
	for _, stmt := range stmts {
		fmt.Fprintf(&b, "go func() {\n\tdefer _wg.Done()\n\t%s\n}()\n", stmt)
	}
	b.WriteString("_wg.Wait()")
	return b.String()
}

// throttle 100 { body } -> site-scoped token bucket gate
func (t *fwTranspiler) emitThrottle(n *gotreesitter.Node) string {
	rateNode := t.childByField(n, "rate")
	if rateNode == nil {
		return t.text(n)
	}
	t.needsTime = true
	t.needsSync = true
	t.needsThrottle = true

	rate := t.emit(rateNode)
	burst := "1"
	if burstNode := t.childByField(n, "burst"); burstNode != nil {
		burst = t.emit(burstNode)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "// throttle: %s/s, burst: %s\n", rate, burst)
	fmt.Fprintf(&b, "_fwThrottleWait(%q, int(%s), int(%s))\n", t.throttleSiteID(n), rate, burst)
	b.WriteString(t.findBlock(n))
	return b.String()
}

// retry 3 { body } -> retry loop with exponential backoff
func (t *fwTranspiler) emitRetry(n *gotreesitter.Node) string {
	countNode := t.childByField(n, "count")
	if countNode == nil {
		return t.text(n)
	}
	t.needsTime = true

	block := "{}"
	if blockNode := t.findBlockNode(n); blockNode != nil {
		block = t.withTryTarget(tryTarget{kind: tryTargetRetry}, func() string {
			return t.emit(blockNode)
		})
	}

	delay := "100"
	backoff := "2"
	if delayNode := t.childByField(n, "delay"); delayNode != nil {
		delay = t.emit(delayNode)
	}
	if backoffNode := t.childByField(n, "backoff"); backoffNode != nil {
		backoff = t.emit(backoffNode)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "// retry %s times (delay: %sms, backoff: %sx)\n", t.emit(countNode), delay, backoff)
	b.WriteString("var _retryErr error\n")
	fmt.Fprintf(&b, "_retryDelay := time.Duration(%s) * time.Millisecond\n", delay)
	fmt.Fprintf(&b, "for _attempt := 0; _attempt < %s; _attempt++ {\n", t.emit(countNode))
	fmt.Fprintf(&b, "\t_retryErr = func() error {\n\t\t%s\n\t\treturn nil\n\t}()\n", block)
	b.WriteString("\tif _retryErr == nil { break }\n")
	b.WriteString("\ttime.Sleep(_retryDelay)\n")
	fmt.Fprintf(&b, "\t_retryDelay = time.Duration(float64(_retryDelay) * %s)\n", backoff)
	b.WriteString("}\n")
	b.WriteString("_ = _retryErr")
	return b.String()
}

// breaker "service" { body } -> circuit breaker logic
func (t *fwTranspiler) emitBreaker(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	if nameNode == nil {
		return t.text(n)
	}
	t.needsTime = true
	t.needsSync = true
	t.needsBreaker = true

	nameStr := t.text(nameNode)
	varName := strings.Trim(nameStr, `"`)
	varName = strings.NewReplacer("-", "_", " ", "_").Replace(varName)
	block := t.findBlock(n)

	threshold := "5"
	cooldown := "30"
	if thresholdNode := t.childByField(n, "threshold"); thresholdNode != nil {
		threshold = t.emit(thresholdNode)
	}
	if cooldownNode := t.childByField(n, "cooldown"); cooldownNode != nil {
		cooldown = t.emit(cooldownNode)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "// Circuit breaker %s (threshold: %s failures, cooldown: %ss)\n", nameStr, threshold, cooldown)
	fmt.Fprintf(&b, "_fwBreakerDo(%q, %s, %s*time.Second, func() {\n", varName, threshold, cooldown)
	fmt.Fprintf(&b, "\t%s\n", block)
	fmt.Fprintf(&b, "})")
	return b.String()
}
