package vector

// MathTier names the vector math implementation this binary runs: "avx2",
// "neon", or "scalar" (no SIMD — another CPU, or a build with -tags noasm).
// The diagnose bundle reports it (OPS-025): a slow vector search on a scalar
// build is a different report from one on an accelerated build.
func MathTier() string { return vectorMathTier() }
