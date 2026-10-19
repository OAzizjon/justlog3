# Node.js logging benchmark

Node: v24.19.0; OS: win32 10.0.26200 x64
CPU: Intel(R) Core(TM) Ultra 7 155H; RAM: 16866729984 bytes
Calls/run: 50,000; runs/case: 7; fresh process per run
Timing includes logging, final flush/close and console drain; excludes setup and verification.
Console stdout goes to the OS null device. No fsync or physical-disk durability guarantee.

| Scenario | Logger | Median (µs/call) | Min–max (µs/call) |
| --- | --- | ---: | ---: |
| file | justlog3 | 4.175 | 3.265–5.046 |
| file | pino async | 6.029 | 5.520–6.721 |
| file | pino sync | 5.741 | 5.105–6.099 |
| file | winston | 5.269 | 5.105–5.723 |
| file | fs.appendFileSync | 139.424 | 123.869–188.801 |
| file + console | justlog3 | 6.187 | 5.450–6.984 |
| file + console | pino async | 6.986 | 6.861–8.406 |
| file + console | pino sync | 6.570 | 5.829–7.762 |
| file + console | winston | 6.404 | 6.083–7.086 |
| file + console | fs.appendFileSync | 144.829 | 136.256–198.242 |
