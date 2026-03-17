;; Auto-generated highlight queries for grammar extension
;; Extension: ferrous_wheel (extends go)

;; Keywords
"enum" @keyword
"fn" @keyword
"let" @keyword
"match" @keyword
"try" @keyword

;; Operators
"=>" @operator
"?" @operator
"?." @operator
"??" @operator

;; enum_variant
(enum_variant name: (identifier) @constructor)

;; enum_declaration
(enum_declaration name: (identifier) @type.definition)

;; ternary_expression
(ternary_expression "?" @operator ":" @operator)

;; match_arm
(match_arm pattern: (identifier) @constant)
(match_arm "if" @keyword)

;; match_expression
(match_expression subject: (identifier) @variable)

;; let_declaration
(let_declaration name: (identifier) @variable.definition)

;; let_multi_declaration

;; lambda_expression
(lambda_expression (lambda_params (identifier) @variable.parameter))

