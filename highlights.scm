;; Ferrous Wheel highlight queries
;; Extends Go's built-in highlights with 33 language features.
;; These queries layer on top of the base Go grammar's highlights.

;; ─── Keywords ────────────────────────────────────────────────────────────────

;; Type system
"enum" @keyword
"derive" @keyword
"impl" @keyword
"packed" @keyword

;; Bindings
"let" @keyword
"fn" @keyword

;; Pattern matching
"match" @keyword

;; Control flow
"guard" @keyword
"unless" @keyword
"until" @keyword
"repeat" @keyword
"swap" @keyword

;; Error handling
"try" @keyword

;; Low-level
"arena" @keyword
"pin" @keyword
"unpin" @keyword
"mmap" @keyword
"vectorize" @keyword

;; Concurrency
"concurrent" @keyword
"throttle" @keyword
"retry" @keyword
"breaker" @keyword

;; ─── Operators ───────────────────────────────────────────────────────────────

"=>" @operator
"?." @operator
"??" @operator
".." @operator
"|>" @operator

(ternary_expression "?" @operator ":" @operator)

;; ─── Type system ─────────────────────────────────────────────────────────────

(enum_declaration name: (identifier) @type.definition)
(enum_variant name: (identifier) @constructor)

(derive_declaration trait: (identifier) @type)
(derive_declaration "for" @keyword)
(derive_declaration type: (identifier) @type)

(impl_block type: (identifier) @type)

;; ─── Pattern matching ────────────────────────────────────────────────────────

(match_expression subject: (identifier) @variable)
(match_arm pattern: (identifier) @constant)
(match_arm "=>" @operator)
(match_arm "if" @keyword)

(if_let_statement "if" @keyword)
(if_let_statement pattern: (identifier) @variable.definition)

;; ─── Bindings ────────────────────────────────────────────────────────────────

(let_declaration name: (identifier) @variable.definition)
(let_multi_declaration (identifier) @variable.definition)
(lambda_expression (lambda_params (identifier) @variable.parameter))

;; ─── Expressions ─────────────────────────────────────────────────────────────

(safe_navigation field: (identifier) @property)
(fstring "f" @string.special)

(list_comprehension "for" @keyword)
(list_comprehension "in" @keyword)
(list_comprehension "if" @keyword)
(list_comprehension var: (identifier) @variable.definition)

;; ─── Control flow ────────────────────────────────────────────────────────────

(for_in_statement "in" @keyword)
(for_in_statement var: (identifier) @variable.definition)
(for_in_index_statement "in" @keyword)
(for_in_index_statement index: (identifier) @variable.definition)
(for_in_index_statement var: (identifier) @variable.definition)

(repeat_statement count: (_) @number)

(defer_error "defer!" @keyword)

;; ─── Low-level memory ────────────────────────────────────────────────────────

(arena_block name: (identifier) @variable.definition)

(pin_statement name: (identifier) @variable)
(unpin_statement name: (identifier) @variable)

(unsafe_cast "unsafe" @keyword)
(unsafe_cast "cast" @function.builtin)

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

(throttle_block rate: (_) @number)
(retry_block count: (_) @number)
(breaker_block name: (_) @string)
