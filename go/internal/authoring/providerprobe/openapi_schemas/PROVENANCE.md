# Frozen OpenAPI validation schemas

These files are verbatim copies of the schema assets shipped by
`@apidevtools/openapi-schemas@2.1.0` (MIT; see `LICENSE`). The vendored OAI
schema material retains its Apache-2.0 notice in
`LICENSE-OAI-APACHE-2.0`. They support the
legacy-only compatibility validator in `legacy_openapi_validate.go`; they are
not an input to the qualified/source-first path.

The matching Node validator is `@apidevtools/swagger-parser@12.1.0`, whose
schema and supplemental-spec sources are identified below.  The pinned
ref-parser and method-list package versions are recorded because their
dereference and operation enumeration behavior define the compatibility
boundary.

| Asset | SHA-256 |
| --- | --- |
| `v2.0/schema.json` | `b36871c8016292c5e66dd3b203e69aeff98bfef97e0b3c67c1909036095586a5` |
| `v3.0/schema.json` | `d03136244e74914d37003908554bf184c4496c6a8fe03fb3910c810561a86bed` |
| `v3.1/schema.json` | `eb5c4544fa2560f8dbd25da98014b7efc07d1ab1e6d7320afec559ee0df2a1fc` |
| `LICENSE` | `e6004a376e1b492862c0a9f036606e848c082e093f66a1baf824b17b80064f24` |
| `LICENSE-OAI-APACHE-2.0` | `73ba74dfaa520b49a401b5d21459a8523a146f3b7518a833eea5efa85130bf68` |
| swagger-parser `lib/validators/schema.js` | `42c173ec61dfb01dfeaea675704f1bf9fa65e14ca99ea83033adcc5333c37d87` |
| swagger-parser `lib/validators/spec.js` | `2d6facdd611b07796804d4e9d53f191eb71318cba860e0a323f1127f79f650f8` |
| swagger-parser `lib/index.js` | `5063aa7c240efd462c9976b935be9554b40aaf2d0f7cb2af1c3fbcb00ba8a94b` |
| json-schema-ref-parser `dist/lib/index.js` | `a11292f5a5fa8d402df37080d437907823d1a434b663f2b47fd6895bda1bd0d1` |
| swagger-methods `lib/index.js` | `d744efa06275ae14dedcffe1a864d4c2bfccc41a95d9834a0bf420b6748ba604` |

The source package versions are swagger-parser 12.1.0,
json-schema-ref-parser 14.0.1, and swagger-methods 3.0.2.

`../testdata/legacy_openapi_node_oracle.json` is the 90-serializable-case
offline replay corpus derived from an independent local execution of that
frozen Node stack. Its SHA-256 is
`fc9e0f6fbaa804af62738b514260f18e4fcd04d98fdfbe0f24a5af67d6080f0c`.
The source corpus script SHA-256 is
`e4bd570706967632bed7daac56875963099bcc266e2dd377d21704c24fca24eb`;
the 91-case Node result SHA-256 is
`b8b174628d6b6004d557f0b77a7aa655de0475f8b296eb01b672449ffdcf6a2f`.
The companion 18-case direct `spec.js` replay corpus is
`../testdata/legacy_openapi_node_direct_spec_oracle.json`, SHA-256
`bf6daae6d0de5315740d6e97b655ee70e5ef3395296d38ace7999e005355d459`.
