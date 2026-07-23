# Frozen OpenAPI validation schemas

These files are verbatim copies of the schema assets shipped by
`@apidevtools/openapi-schemas@2.1.0` (MIT; see `LICENSE`). The vendored OAI
schema material retains its Apache-2.0 notice in
`LICENSE-OAI-APACHE-2.0`. They support the
legacy-only compatibility validator in `legacy_openapi_validate.go`; they are
not an input to the qualified/source-first path.

The v1 compatibility behavior was defined by
`@apidevtools/swagger-parser@12.1.0`. Its schema and supplemental-spec sources
are identified below. The pinned ref-parser and method-list package versions
remain part of the documented compatibility boundary.

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
json-schema-ref-parser 14.0.1, and swagger-methods 3.0.2. Current behavior is
covered directly in `legacy_openapi_validate_test.go`; there is no replay
fixture or retired runtime dependency.
