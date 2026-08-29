# LLVM

LLVM compiler infrastructure.

- IR design: SSA, basic blocks, control-flow and dataflow analysis
- Pass pipeline: optimization passes, ordering, phase-ordering hazards
- Target description: instruction selection, scheduling, register allocation
- MLIR for higher-level dialects layered above LLVM IR
- Tooling: `opt`, `llc`, `clang -emit-llvm`, address sanitizer integration
