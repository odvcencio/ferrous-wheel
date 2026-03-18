;; Ferrous Wheel highlight queries — all parent-scoped for reliability.

;; ─── Type system ─────────────────────────────────────────────────────────────
(enum_declaration "enum" @keyword)
(enum_declaration name: (identifier) @type.definition)
(enum_variant name: (identifier) @constructor)

(derive_declaration "derive" @keyword)
(derive_declaration trait: (identifier) @type)
(derive_declaration "for" @keyword)
(derive_declaration type: (identifier) @type)

(impl_block "impl" @keyword)
(impl_block type: (identifier) @type)

;; ─── Bindings ────────────────────────────────────────────────────────────────
(let_declaration "let" @keyword)
(let_declaration name: (identifier) @variable.definition)

(let_multi_declaration "let" @keyword)
(let_multi_declaration (identifier) @variable.definition)

(lambda_expression "fn" @keyword)
(lambda_expression (lambda_params (identifier) @variable.parameter))

;; ─── Pattern matching ────────────────────────────────────────────────────────
(match_expression "match" @keyword)
(match_expression subject: (identifier) @variable)
(match_arm pattern: (identifier) @constant)
(match_arm "=>" @operator)
(match_arm "if" @keyword)

(if_let_statement "if" @keyword)
(if_let_statement "let" @keyword)
(if_let_statement pattern: (identifier) @variable.definition)

;; ─── Expressions ─────────────────────────────────────────────────────────────
(error_propagation "try" @keyword)

(safe_navigation "?." @operator)
(safe_navigation field: (identifier) @property)

(null_coalesce "??" @operator)

(ternary_expression "?" @operator)
(ternary_expression ":" @operator)

(fstring "f" @string.special)

(list_comprehension "for" @keyword)
(list_comprehension "in" @keyword)
(list_comprehension "if" @keyword)
(list_comprehension var: (identifier) @variable.definition)

(pipeline_expression (identifier) @function)

;; ─── Control flow ────────────────────────────────────────────────────────────
(for_in_statement "for" @keyword)
(for_in_statement "in" @keyword)
(for_in_statement var: (identifier) @variable.definition)

(for_in_index_statement "for" @keyword)
(for_in_index_statement "in" @keyword)
(for_in_index_statement index: (identifier) @variable.definition)
(for_in_index_statement var: (identifier) @variable.definition)

(range_expression ".." @operator)

(guard_statement "guard" @keyword)
(guard_statement "else" @keyword)

(unless_statement "unless" @keyword)
(until_statement "until" @keyword)

(repeat_statement "repeat" @keyword)
(repeat_statement count: (_) @number)

(swap_statement "swap" @keyword)
(defer_error "defer!" @keyword)

;; ─── Low-level memory ────────────────────────────────────────────────────────
(arena_block "arena" @keyword)
(arena_block name: (identifier) @variable.definition)

(pin_statement "pin" @keyword)
(pin_statement name: (identifier) @variable)

(unpin_statement "unpin" @keyword)
(unpin_statement name: (identifier) @variable)

(unsafe_cast "unsafe" @keyword)
(unsafe_cast "cast" @function.builtin)

(mmap_block "mmap" @keyword)
(mmap_block "file" @keyword)
(mmap_block "as" @keyword)
(mmap_block name: (identifier) @variable.definition)
(mmap_block path: (_) @string)

(packed_annotation "packed" @attribute)
(vectorize_statement "vectorize" @attribute)

;; ─── Concurrency ─────────────────────────────────────────────────────────────
(select_block "select!" @keyword)
(select_arm var: (identifier) @variable.definition)
(select_arm "from" @keyword)
(select_arm chan: (identifier) @variable)
(select_arm "=>" @operator)
(select_arm "timeout" @keyword)
(select_arm "default" @keyword)

(fan_out_block "fan" @keyword)
(fan_out_block "out" @keyword)
(fan_out_block name: (identifier) @variable.definition)
(fan_out_block count: (_) @number)

(fan_in_expression "fan" @keyword)
(fan_in_expression "in" @keyword)

(concurrent_block "concurrent" @keyword)

(throttle_block "throttle" @keyword)
(throttle_block rate: (_) @number)

(retry_block "retry" @keyword)
(retry_block count: (_) @number)

(breaker_block "breaker" @keyword)
(breaker_block name: (_) @string)
