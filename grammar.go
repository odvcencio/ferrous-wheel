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

		// Wire into grammar
		AppendChoice(g, "_top_level_declaration",
			Sym("enum_declaration"),
		)

		AppendChoice(g, "_expression",
			Sym("match_expression"),
			Sym("null_coalesce"),
			Sym("safe_navigation"),
			Sym("error_propagation"),
			Sym("lambda_expression"),
			Sym("ternary_expression"),
		)

		AppendChoice(g, "_statement",
			Sym("let_declaration"),
			Sym("let_multi_declaration"),
			Sym("enum_declaration"),
			Sym("match_expression"),
		)

		// Mark new keywords as non-keyword strings
		g.NonKeywordStrings["if"] = true // used in match guards, also Go keyword

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

		g.EnableLRSplitting = true
	})
}
