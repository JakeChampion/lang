export const demos = {
  garden: {
    name: "Pixel garden",
    description:
      "A four-colour concentric garden. The ROM paints a row on each screen event; every instruction runs in Fern.",
    source: `( Pixel garden — a screen vector paints one row per event. )
|00 @row $1 @column $1 @phase $1
|0100
  #124f #08 DEO2 ( red palette )
  #239c #0a DEO2 ( green palette )
  #2465 #0c DEO2 ( blue palette )
  #0080 #22 DEO2 #0080 #24 DEO2
  ;on-screen #20 DEO2
  BRK

@on-screen
  #00 .column STZ
  &loop
    .column LDZ #00 SWP #28 DEO2
    .row LDZ #00 SWP #2a DEO2
    #00 .column LDZ #0040 SUB2 DUP2 MUL2
    #00 .row LDZ #0040 SUB2 DUP2 MUL2
    ADD2 #08 SFT2 NIP
    .phase LDZ ADD #03 AND #2e DEO
    .column LDZ INC DUP .column STZ
    #80 LTH ?&loop
  .row LDZ INC #7f AND DUP .row STZ
  #00 NEQ ?{ .phase LDZ INC .phase STZ }
  BRK`,
  },
  hello: {
    name: "Hello, Uxn",
    description:
      "Follow a string pointer through memory. LDAk preserves the pointer; Console/write prints each byte.",
    source: `( Hello from a tiny computer. Select Step to follow the loop. )
|0100
  ;message
  &loop
    LDAk DUP ?{ POP POP2 BRK }
    #18 DEO INC2 !&loop
@message "Hello, 20 "Uxn! 0a "Running 20 "inside 20 "Fern 20 "WebAssembly. 0a 00`,
  },
  arithmetic: {
    name: "Stack arithmetic",
    description:
      "Step through a six-factorial loop. The result 0x02d0 (720) remains on the working stack and at RAM 0x0200.",
    source: `( Factorial: accumulator* counter* -- result* )
|0100
  #0001 #0006
  &loop
    SWP2 OVR2 MUL2 SWP2
    #0001 SUB2 DUP2 #0000 NEQ2 ?&loop
  POP2
  DUP2 #0200 STA2
  BRK`,
  },
  paint: {
    name: "Mouse painter",
    description:
      "Drag across the screen or use the touch surface to stamp hearts. The mouse vector and sprite drawing run in Fern.",
    source: `( Two layers, an 8x8 sprite, and a mouse vector. )
|0100
  #f07f #08 DEO2 #f0d6 #0a DEO2 #f0b2 #0c DEO2
  #0100 #22 DEO2 #00c0 #24 DEO2
  ;on-mouse #90 DEO2
  ;heart #2c DEO2
  #007c #28 DEO2 #005c #2a DEO2
  #03 #2f DEO
  BRK
@on-mouse
  #96 DEI #01 AND ?{ BRK }
  #92 DEI2 #28 DEO2
  #94 DEI2 #2a DEO2
  ;heart #2c DEO2
  #03 #2f DEO
  BRK
@heart 00 66 ff ff ff 7e 3c 18`,
  },
  echo: {
    name: "Console echo",
    description:
      "The console input vector echoes each UTF-8 byte. Enter a line below the console and send it to the ROM.",
    source: `( Device events preserve both stacks and RAM. )
|0100
  ;on-input #10 DEO2 BRK
@on-input
  #12 DEI #18 DEO BRK`,
  },
};
