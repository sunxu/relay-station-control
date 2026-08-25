package auth

import "testing"

const benchmarkPassword = "correct horse battery staple benchmark"

func requireBenchmarkArgon2idFloor(b *testing.B) {
	b.Helper()
	if CurrentArgon2Params.Memory != 64*1024 || CurrentArgon2Params.Iterations != 3 {
		b.Fatalf("Argon2id benchmark security parameters changed: memory=%d KiB iterations=%d", CurrentArgon2Params.Memory, CurrentArgon2Params.Iterations)
	}
}

func BenchmarkArgon2idHashSerial(b *testing.B) {
	requireBenchmarkArgon2idFloor(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := HashPassword(benchmarkPassword); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkArgon2idVerifySerial(b *testing.B) {
	requireBenchmarkArgon2idFloor(b)
	phc, err := HashPassword(benchmarkPassword)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		matched, upgrade, verifyErr := VerifyPassword(benchmarkPassword, phc)
		if verifyErr != nil || !matched || upgrade {
			b.Fatalf("VerifyPassword() = matched %v, upgrade %v, error %v", matched, upgrade, verifyErr)
		}
	}
}

func BenchmarkArgon2idHashVerifyTargetConcurrency(b *testing.B) {
	requireBenchmarkArgon2idFloor(b)
	b.ReportAllocs()
	b.RunParallel(func(worker *testing.PB) {
		for worker.Next() {
			phc, err := HashPassword(benchmarkPassword)
			if err != nil {
				b.Error(err)
				continue
			}
			matched, upgrade, verifyErr := VerifyPassword(benchmarkPassword, phc)
			if verifyErr != nil || !matched || upgrade {
				b.Errorf("VerifyPassword() = matched %v, upgrade %v, error %v", matched, upgrade, verifyErr)
			}
		}
	})
}
