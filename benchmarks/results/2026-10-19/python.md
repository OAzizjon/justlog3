Python 3.14.6 on Windows-11-10.0.26200-SP0; Intel64 Family 6 Model 170 Stepping 4, GenuineIntel, 22 logical CPUs
justlog3 from ..\..\python\src; loguru 0.7.3
50,000 calls per run, 7 runs, libraries interleaved within each round; median (min-max), microseconds per call

| Scenario | justlog3 | logging | loguru |
|---|---|---|---|
| File only | 5.4 (5.3-5.6) | 16.2 (16.0-16.5) | 17.2 (17.0-17.5) |
| File + console | 7.1 (6.7-16.1) | 22.9 (21.6-47.5) | 23.3 (22.2-47.0) |
| File only, 8 threads | 6.9 (6.7-10.9) | 26.2 (24.6-32.2) | 26.5 (25.1-29.1) |
| File + cloud (local server) | 7.3 (6.5-7.5) | - | - |
