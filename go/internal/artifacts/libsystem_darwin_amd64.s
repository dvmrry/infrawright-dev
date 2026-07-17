//go:build darwin && !ios && amd64

#include "textflag.h"

TEXT libc_artifacts_openat_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_artifacts_openat(SB)
GLOBL	·libcArtifactsOpenatTrampolineAddr(SB), RODATA, $8
DATA	·libcArtifactsOpenatTrampolineAddr(SB)/8, $libc_artifacts_openat_trampoline<>(SB)

TEXT libc_artifacts_fgetattrlist_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_artifacts_fgetattrlist(SB)
GLOBL	·libcArtifactsFgetattrlistTrampolineAddr(SB), RODATA, $8
DATA	·libcArtifactsFgetattrlistTrampolineAddr(SB)/8, $libc_artifacts_fgetattrlist_trampoline<>(SB)

TEXT libc_artifacts_fstatat64_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_artifacts_fstatat64(SB)
GLOBL	·libcArtifactsFstatat64TrampolineAddr(SB), RODATA, $8
DATA	·libcArtifactsFstatat64TrampolineAddr(SB)/8, $libc_artifacts_fstatat64_trampoline<>(SB)
