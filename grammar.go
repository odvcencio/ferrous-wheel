package ferrouswheel

// Grammar extends Go with Rust-inspired syntax sugar.
// File extension: .fw
//
// Features:
//
//	enum Color { Red, Green, Blue(int) }       -> struct + const + constructors
//	match color { Red => ..., Blue(n) => ... } -> switch
//	match x { n if n > 0 => "pos" }           -> switch with guard clauses
//	val := try doSomething()                   -> if err != nil { return ..., err }
//	obj?.field                                 -> nil check + field access
//	val ?? default                             -> if val != nil { ... } else { default }
//	let x = 1                                  -> x := 1
//	let (a, b) = f()                           -> a, b := f()
//	cond ? trueVal : falseVal                  -> IIFE ternary
//	fn(x) x * 2                               -> func(x) { return x * 2 }
//	Result[T] / Option[T]                      -> auto-injected generic types
//	derive Stringer for Color                  -> auto-generate interface impls
//	if let x = expr { ... }                    -> pattern destructuring
//	for i in 0..10 { }                         -> range-based for loops
//	f"hello {name}"                            -> fmt.Sprintf interpolation
//	guard cond else return err                 -> early return
//	defer! f.Close()                           -> error-capturing defer
//	impl Type { fn ... }                       -> method grouping
//	unless cond { }                            -> negated if
//	until cond { }                             -> negated for
//	repeat 5 { }                               -> counted loop
//	[x*2 for x in items if x > 0]             -> slice comprehension
//	swap(a, b)                                 -> tuple swap
func Grammar() *GrammarType {
	return ExtendGrammar("ferrous_wheel", GoGrammar(), func(g *GrammarType) {

		// Mark ferrous-wheel keywords as non-keyword strings so the keyword DFA
		// doesn't promote them unconditionally (they coexist as identifiers
		// in Go code).
		if g.NonKeywordStrings == nil {
			g.NonKeywordStrings = make(map[string]bool)
		}
		g.NonKeywordStrings["enum"] = true
		g.NonKeywordStrings["match"] = true
		g.NonKeywordStrings["let"] = true
		g.NonKeywordStrings["fn"] = true
		g.NonKeywordStrings["try"] = true

		// --- enum declaration ---
		// enum Color { Red, Green, Blue(int) }
		g.Define("enum_variant", Choice(
			// Variant with payload types: Blue(int)
			PrecDynamic(10, Seq(
				Field("name", Sym("identifier")),
				Str("("),
				CommaSep1(Sym("_type")),
				Str(")"),
			)),
			// Simple variant: Red
			Field("name", Sym("identifier")),
		))

		g.Define("enum_declaration", Seq(
			Str("enum"),
			Field("name", Sym("identifier")),
			Str("{"),
			CommaSep1(Sym("enum_variant")),
			Optional(Str(",")), // trailing comma
			Str("}"),
		))

		// --- ternary expression ---
		// cond ? trueVal : falseVal
		// PrecRight(1) so nested ternaries associate right:
		//   a ? b : c ? d : e  =>  a ? b : (c ? d : e)
		g.Define("ternary_expression", PrecRight(1, Seq(
			Field("condition", Sym("_expression")),
			Str("?"),
			Field("consequence", Sym("_expression")),
			Str(":"),
			Field("alternative", Sym("_expression")),
		)))

		// --- match expression ---
		// match expr { Pattern => body, ... }
		// Match arms support optional guard clauses: pattern if guard => body
		g.Define("match_arm", Seq(
			Field("pattern", Sym("_expression")),
			Optional(Seq(Str("if"), Field("guard", Sym("_expression")))),
			Str("=>"),
			Field("body", Choice(Sym("block"), Sym("_expression"))),
		))

		g.Define("match_expression", PrecDynamic(15, Seq(
			Str("match"),
			Field("subject", Sym("_expression")),
			Str("{"),
			CommaSep1(Sym("match_arm")),
			Optional(Str(",")), // trailing comma
			Str("}"),
		)))

		// --- null coalescing: expr ?? default ---
		// PrecLeft(2) puts it below most Go operators but above logical OR.
		// The ?? operator is a named token rule to ensure it matches as a
		// single 2-char token and doesn't get split into two ? tokens.
		g.Define("_fw_null_coalesce_op", Token(Seq(Str("?"), Str("?"))))

		g.Define("null_coalesce", PrecLeft(2,
			Seq(
				Field("left", Sym("_expression")),
				Sym("_fw_null_coalesce_op"),
				Field("right", Sym("_expression")),
			),
		))

		// --- safe navigation: expr?.field ---
		// ImmToken ensures ?. is treated as a single immediate token attached
		// to the preceding expression without whitespace.
		// PrecLeft(8) binds tighter than most operators.
		g.Define("safe_navigation", PrecLeft(8,
			Seq(
				Field("object", Sym("_expression")),
				ImmToken(Str("?.")),
				Field("field", Sym("identifier")),
			),
		))

		// --- error propagation: try expr ---
		// Uses try prefix instead of ? suffix to avoid DFA conflict with ??
		// try doSomething()  ->  if err != nil { return ..., err }
		g.Define("error_propagation", PrecDynamic(-1,
			Seq(
				Str("try"),
				Field("expr", Sym("_expression")),
			),
		))

		// --- let binding: let x = expr ---
		g.Define("let_declaration", Seq(
			Str("let"),
			Field("name", Sym("identifier")),
			Str("="),
			Field("value", Sym("_expression")),
		))

		// --- let multi-assignment: let (a, b) = f() ---
		// Transpiles to: a, b := f()
		g.Define("let_multi_declaration", Seq(
			Str("let"),
			Str("("),
			CommaSep1(Sym("identifier")),
			Str(")"),
			Str("="),
			Field("value", Sym("_expression")),
		))

		// --- lambda: fn(params) body ---
		// Uses fn keyword to avoid conflict with bitwise OR operator |.
		// fn(x, y) x + y  or  fn(x) { return x * 2 }
		g.Define("lambda_params", Seq(
			Str("("),
			CommaSep1(Sym("identifier")),
			Str(")"),
		))

		// _lambda_body is a thin wrapper that gives the lambda body very low
		// precedence, forcing binary operators to be absorbed into it rather
		// than wrapping the lambda.
		g.Define("_lambda_body", PrecRight(-100, Sym("_expression")))

		g.Define("lambda_expression", PrecRight(-1, Seq(
			Str("fn"),
			Field("params", Sym("lambda_params")),
			Field("body", Choice(Sym("block"), Sym("_lambda_body"))),
		)))

		// --- derive declaration ---
		// derive Stringer for Color
		g.Define("derive_declaration", Seq(
			Str("derive"),
			Field("trait", Sym("identifier")),
			Str("for"),
			Field("type", Sym("identifier")),
		))

		// --- if let statement ---
		// if let x = expr { body } else { body }
		g.Define("if_let_statement", Seq(
			Str("if"),
			Str("let"),
			Field("pattern", Sym("identifier")),
			Str("="),
			Field("value", Sym("_expression")),
			Sym("block"),
			Optional(Seq(Str("else"), Sym("block"))),
		))

		// --- range expression ---
		// 0..10
		// Use Token for ".." to avoid conflict with Go's "." selector and float parsing.
		g.Define("_fw_range_op", Token(Seq(Str("."), Str("."))))

		g.Define("range_expression", PrecLeft(3, Seq(
			Field("start", Sym("_expression")),
			Sym("_fw_range_op"),
			Field("end", Sym("_expression")),
		)))

		// --- for in statement (single variable) ---
		// for v in iterable { body }
		g.Define("for_in_statement", Seq(
			Str("for"),
			Field("var", Sym("identifier")),
			Str("in"),
			Field("iterable", Sym("_expression")),
			Sym("block"),
		))

		// --- for in with index ---
		// for i, v in iterable { body }
		g.Define("for_in_index_statement", Seq(
			Str("for"),
			Field("index", Sym("identifier")),
			Str(","),
			Field("var", Sym("identifier")),
			Str("in"),
			Field("iterable", Sym("_expression")),
			Sym("block"),
		))

		// --- f-string ---
		// f"hello {name}, you are {age} years old"
		// Defined as a single token pattern to avoid "f" conflicting with identifiers.
		g.Define("fstring", Token(Pat(`f"[^"]*"`)))

		// --- guard statement ---
		// guard cond else return err
		g.Define("guard_statement", Seq(
			Str("guard"),
			Field("condition", Sym("_expression")),
			Str("else"),
			Sym("block"),
		))

		// --- defer! error-capturing defer ---
		// defer! f.Close()
		g.Define("_fw_defer_bang", Token(Seq(Str("defer"), Str("!"))))

		g.Define("defer_error", Seq(
			Sym("_fw_defer_bang"),
			Field("expr", Sym("_expression")),
		))

		// --- impl block ---
		// impl Type { fn methods... }
		g.Define("impl_block", Seq(
			Str("impl"),
			Field("type", Sym("identifier")),
			Sym("block"),
		))

		// --- unless statement ---
		// unless cond { body }
		g.Define("unless_statement", Seq(
			Str("unless"),
			Field("condition", Sym("_expression")),
			Sym("block"),
		))

		// --- until statement ---
		// until cond { body }
		g.Define("until_statement", Seq(
			Str("until"),
			Field("condition", Sym("_expression")),
			Sym("block"),
		))

		// --- repeat statement ---
		// repeat 5 { body }
		g.Define("repeat_statement", Seq(
			Str("repeat"),
			Field("count", Sym("_expression")),
			Sym("block"),
		))

		// --- list comprehension ---
		// [x * 2 for x in items if x > 0]
		g.Define("list_comprehension", Seq(
			Str("["),
			Field("expr", Sym("_expression")),
			Str("for"),
			Field("var", Sym("identifier")),
			Str("in"),
			Field("iterable", Sym("_expression")),
			Optional(Seq(Str("if"), Field("filter", Sym("_expression")))),
			Str("]"),
		))

		// --- swap statement ---
		// swap(a, b)
		g.Define("swap_statement", Seq(
			Str("swap"),
			Str("("),
			Field("a", Sym("_expression")),
			Str(","),
			Field("b", Sym("_expression")),
			Str(")"),
		))

		// =============================================
		// LOW-LEVEL MEMORY MANAGEMENT FEATURES
		// =============================================

		// --- arena block: bump allocator ---
		// arena scratch { body } or arena scratch 1024*1024 { body }
		g.Define("arena_block", Seq(
			Str("arena"),
			Field("name", Sym("identifier")),
			Optional(Field("size", Sym("_expression"))),
			Sym("block"),
		))

		// --- pin/unpin: GC pinning ---
		// pin data / unpin data
		g.Define("pin_statement", Seq(
			Str("pin"),
			Field("name", Sym("identifier")),
		))
		g.Define("unpin_statement", Seq(
			Str("unpin"),
			Field("name", Sym("identifier")),
		))

		// --- unsafe cast: zero-copy type conversion ---
		// unsafe cast(expr, TargetType)
		g.Define("unsafe_cast", Seq(
			Str("unsafe"),
			Str("cast"),
			Str("("),
			Field("expr", Sym("_expression")),
			Str(","),
			Field("target_type", Sym("_type")),
			Str(")"),
		))

		// --- mmap block: memory-mapped file ---
		// mmap file "data.bin" as data []byte { body }
		g.Define("mmap_block", Seq(
			Str("mmap"),
			Str("file"),
			Field("path", Sym("_string_literal")),
			Str("as"),
			Field("name", Sym("identifier")),
			Field("type", Sym("_type")),
			Sym("block"),
		))

		// --- packed annotation ---
		// packed struct Packet { ... }
		g.Define("packed_annotation", Seq(
			Str("packed"),
			Field("decl", Sym("_statement")),
		))

		// --- vectorize hint ---
		// vectorize for v in items { body }
		g.Define("vectorize_statement", Seq(
			Str("vectorize"),
			Str("for"),
			Field("var", Sym("identifier")),
			Str("in"),
			Field("range", Sym("_expression")),
			Sym("block"),
		))

		// =============================================
		// CONCURRENCY FEATURES
		// =============================================

		// --- select! with sugar ---
		g.Define("select_arm", Choice(
			Seq(Field("var", Sym("identifier")), Str("from"), Field("chan", Sym("_expression")), Str("=>"), Field("body", Sym("_expression"))),
			Seq(Str("timeout"), Field("duration", Sym("_expression")), Str("=>"), Field("body", Sym("_expression"))),
			Seq(Str("default"), Str("=>"), Field("body", Sym("_expression"))),
		))
		g.Define("select_block", Seq(
			Str("select!"),
			Str("{"),
			Repeat1(Seq(Sym("select_arm"), Optional(Str(",")))),
			Str("}"),
		))

		// --- fan out: goroutine pool ---
		// fan out workers, 10 { body }
		g.Define("fan_out_block", Seq(
			Str("fan"),
			Str("out"),
			Field("name", Sym("identifier")),
			Str(","),
			Field("count", Sym("_expression")),
			Sym("block"),
		))

		// --- fan in: merge channels ---
		// fan in [ch1, ch2, ch3]
		g.Define("fan_in_expression", Seq(
			Str("fan"),
			Str("in"),
			Str("["),
			CommaSep1(Field("channels", Sym("_expression"))),
			Str("]"),
		))

		// --- pipeline: chained channel processing ---
		// data |> filter(valid) |> transform(normalize)
		g.Define("_pipe_op", Token(Seq(Str("|"), Str(">"))))
		g.Define("pipeline_expression", PrecLeft(1, Seq(
			Field("left", Sym("_expression")),
			Sym("_pipe_op"),
			Field("right", Sym("_expression")),
		)))

		// --- concurrent: structured concurrency ---
		// concurrent { stmt1; stmt2 }
		g.Define("concurrent_block", Seq(
			Str("concurrent"),
			Sym("block"),
		))

		// --- throttle: rate limiter ---
		// throttle 100 { body }
		// throttle 100 { } — defaults: burst=1
		// throttle 100 burst 10 { } — explicit burst
		g.Define("throttle_block", Seq(
			Str("throttle"),
			Field("rate", Sym("_expression")),
			Optional(Seq(Str("burst"), Field("burst", Sym("_expression")))),
			Sym("block"),
		))

		// --- retry: with backoff ---
		// retry 3 { body }
		// retry 3 { } — defaults: delay=100ms, backoff=exponential
		// retry 5 delay 500 backoff 2 { } — 500ms initial, 2x multiplier
		g.Define("retry_block", Seq(
			Str("retry"),
			Field("count", Sym("_expression")),
			Optional(Seq(Str("delay"), Field("delay", Sym("_expression")))),
			Optional(Seq(Str("backoff"), Field("backoff", Sym("_expression")))),
			Sym("block"),
		))

		// --- breaker: circuit breaker ---
		// breaker "service" { body }
		// breaker "name" { } — defaults: threshold=5, cooldown=30s
		// breaker "name" threshold 10 cooldown 60 { } — explicit config
		g.Define("breaker_block", Seq(
			Str("breaker"),
			Field("name", Sym("_string_literal")),
			Optional(Seq(Str("threshold"), Field("threshold", Sym("_expression")))),
			Optional(Seq(Str("cooldown"), Field("cooldown", Sym("_expression")))),
			Sym("block"),
		))

		// Wire into grammar
		AppendChoice(g, "_top_level_declaration",
			Sym("enum_declaration"),
			Sym("derive_declaration"),
			Sym("impl_block"),
		)

		AppendChoice(g, "_expression",
			Sym("match_expression"),
			Sym("null_coalesce"),
			Sym("safe_navigation"),
			Sym("error_propagation"),
			Sym("lambda_expression"),
			Sym("ternary_expression"),
			Sym("range_expression"),
			Sym("fstring"),
			Sym("list_comprehension"),
			// Low-level
			Sym("unsafe_cast"),
			// Concurrency
			Sym("fan_in_expression"),
			Sym("pipeline_expression"),
		)

		AppendChoice(g, "_statement",
			Sym("let_declaration"),
			Sym("let_multi_declaration"),
			Sym("enum_declaration"),
			Sym("match_expression"),
			Sym("if_let_statement"),
			Sym("for_in_statement"),
			Sym("for_in_index_statement"),
			Sym("guard_statement"),
			Sym("defer_error"),
			Sym("unless_statement"),
			Sym("until_statement"),
			Sym("repeat_statement"),
			Sym("swap_statement"),
			Sym("derive_declaration"),
			Sym("impl_block"),
			// Low-level
			Sym("arena_block"),
			Sym("pin_statement"),
			Sym("unpin_statement"),
			Sym("mmap_block"),
			Sym("packed_annotation"),
			Sym("vectorize_statement"),
			// Concurrency
			Sym("select_block"),
			Sym("fan_out_block"),
			Sym("concurrent_block"),
			Sym("throttle_block"),
			Sym("retry_block"),
			Sym("breaker_block"),
		)

		// Mark new keywords as non-keyword strings
		g.NonKeywordStrings["if"] = true // used in match guards, also Go keyword
		// New feature keywords that coexist as identifiers in Go code
		g.NonKeywordStrings["derive"] = true
		g.NonKeywordStrings["in"] = true
		g.NonKeywordStrings["guard"] = true
		g.NonKeywordStrings["impl"] = true
		g.NonKeywordStrings["unless"] = true
		g.NonKeywordStrings["until"] = true
		g.NonKeywordStrings["repeat"] = true
		g.NonKeywordStrings["swap"] = true
		// Low-level keywords
		g.NonKeywordStrings["arena"] = true
		g.NonKeywordStrings["pin"] = true
		g.NonKeywordStrings["unpin"] = true
		// Note: "unsafe" is already a Go keyword
		g.NonKeywordStrings["mmap"] = true
		g.NonKeywordStrings["packed"] = true
		g.NonKeywordStrings["vectorize"] = true
		// Concurrency keywords
		// Note: "select" is already a Go keyword; "select!" is a new token
		g.NonKeywordStrings["fan"] = true
		g.NonKeywordStrings["out"] = true
		g.NonKeywordStrings["from"] = true
		g.NonKeywordStrings["timeout"] = true
		g.NonKeywordStrings["concurrent"] = true
		g.NonKeywordStrings["throttle"] = true
		g.NonKeywordStrings["retry"] = true
		g.NonKeywordStrings["breaker"] = true
		// Note: "for" is NOT added — it's already a Go keyword and should be promoted
		// Note: "f" is NOT added — fstring uses a Token(Pat(...)) so no keyword conflict

		// GLR conflicts for keyword ambiguities
		AddConflict(g, "_statement", "let_declaration")
		AddConflict(g, "_statement", "let_multi_declaration")
		AddConflict(g, "_statement", "enum_declaration")
		AddConflict(g, "_statement", "match_expression")
		AddConflict(g, "_expression", "error_propagation")
		AddConflict(g, "_expression", "safe_navigation")
		AddConflict(g, "_expression", "null_coalesce")
		AddConflict(g, "_expression", "lambda_expression")
		AddConflict(g, "_expression", "match_expression")
		AddConflict(g, "_expression", "ternary_expression")

		// ternary ? vs safe_navigation ?. vs null_coalesce ??
		AddConflict(g, "ternary_expression", "safe_navigation")
		AddConflict(g, "ternary_expression", "null_coalesce")
		AddConflict(g, "ternary_expression", "error_propagation")

		// error_propagation ? vs safe_navigation ?. vs null_coalesce ??
		AddConflict(g, "error_propagation", "safe_navigation")
		AddConflict(g, "error_propagation", "null_coalesce")
		AddConflict(g, "safe_navigation", "null_coalesce")

		// match_arm body can be expression or block, conflicts with Go block parsing
		AddConflict(g, "match_arm", "_expression")

		// lambda body vs binary expression: _lambda_body with PrecRight(-100)
		// ensures fn(x) x * 2 parses as fn(x) (x * 2)
		AddConflict(g, "lambda_expression", "binary_expression")

		// let_multi conflicts with let_declaration on the "let" keyword
		AddConflict(g, "let_declaration", "let_multi_declaration")

		// New feature conflicts
		AddConflict(g, "_statement", "if_let_statement")
		AddConflict(g, "_statement", "for_in_statement")
		AddConflict(g, "_statement", "for_in_index_statement")
		AddConflict(g, "_statement", "guard_statement")
		AddConflict(g, "_statement", "defer_error")
		AddConflict(g, "_statement", "unless_statement")
		AddConflict(g, "_statement", "until_statement")
		AddConflict(g, "_statement", "repeat_statement")
		AddConflict(g, "_statement", "swap_statement")
		AddConflict(g, "_statement", "derive_declaration")
		AddConflict(g, "_statement", "impl_block")
		AddConflict(g, "_expression", "range_expression")
		AddConflict(g, "_expression", "list_comprehension")

		// range_expression ".." vs other binary operators
		AddConflict(g, "range_expression", "binary_expression")
		AddConflict(g, "ternary_expression", "range_expression")

		// for_in conflicts with for_in_index on "for" keyword
		AddConflict(g, "for_in_statement", "for_in_index_statement")

		// if_let starts with "if" like Go's if_statement
		AddConflict(g, "if_let_statement", "if_statement")

		// list_comprehension has "for" and "if" inside expressions
		AddConflict(g, "list_comprehension", "_expression")
		AddConflict(g, "list_comprehension", "for_in_statement")

		// impl_block has block like function_declaration
		AddConflict(g, "_top_level_declaration", "impl_block")
		AddConflict(g, "_top_level_declaration", "derive_declaration")

		// --- Low-level feature conflicts ---
		AddConflict(g, "_statement", "arena_block")
		AddConflict(g, "_statement", "pin_statement")
		AddConflict(g, "_statement", "unpin_statement")
		AddConflict(g, "_statement", "mmap_block")
		AddConflict(g, "_statement", "packed_annotation")
		AddConflict(g, "_statement", "vectorize_statement")
		AddConflict(g, "_expression", "unsafe_cast")

		// unsafe_cast starts with "unsafe" which is a Go keyword (unsafe block)
		AddConflict(g, "unsafe_cast", "_expression")

		// packed_annotation wraps a _statement
		AddConflict(g, "packed_annotation", "_statement")

		// vectorize starts with "vectorize for" — "for" conflicts with for_in
		AddConflict(g, "vectorize_statement", "for_in_statement")

		// --- Concurrency feature conflicts ---
		AddConflict(g, "_statement", "select_block")
		AddConflict(g, "_statement", "fan_out_block")
		AddConflict(g, "_statement", "concurrent_block")
		AddConflict(g, "_statement", "throttle_block")
		AddConflict(g, "_statement", "retry_block")
		AddConflict(g, "_statement", "breaker_block")
		AddConflict(g, "_expression", "fan_in_expression")
		AddConflict(g, "_expression", "pipeline_expression")

		// pipeline_expression has PrecLeft(1) like other binary operators
		AddConflict(g, "pipeline_expression", "binary_expression")
		AddConflict(g, "pipeline_expression", "_expression")

		// fan in/out both start with "fan"
		AddConflict(g, "fan_in_expression", "fan_out_block")

		// select_arm body is an expression — conflicts with other expression rules
		AddConflict(g, "select_arm", "_expression")

		g.EnableLRSplitting = true
	})
}
