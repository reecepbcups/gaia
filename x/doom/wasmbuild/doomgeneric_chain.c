// doomgeneric port for the gaia x/doom module.
//
// The host (a cosmos-sdk EndBlocker) drives the game one tic at a time and owns
// the clock, so nothing in here may read wall time, block, or touch a device.
// Everything below is a pure function of the tics and button masks fed in.

#include <stdint.h>
#include <string.h>
#include <stdlib.h>

#include "doomgeneric.h"
#include "doomkeys.h"
#include "d_loop.h"
#include "i_video.h"

#define EXPORT(name) __attribute__((export_name(name)))

extern int usemouse;  // i_video.c, not declared in its header

// wasi-libc has no system(). i_system.c only wants it to pop up a zenity error
// dialog, so failing the probe is the right answer.
int system(const char *command)
{
    (void)command;
    return -1;
}

// DOOM runs at 35 tics/sec. The clock we hand back is derived from the tic
// counter, rounded up so that I_GetTime()'s (ms * 35) / 1000 lands back exactly
// on the tic we meant.
#define TIC_MS(t) (((uint64_t)(t) * 1000u + 34u) / 35u)

#define KEYQUEUE_SIZE 64

static unsigned short s_keyqueue[KEYQUEUE_SIZE];
static unsigned int s_keyqueue_wr;
static unsigned int s_keyqueue_rd;

static uint32_t s_tic;       // tics executed since boot
static uint64_t s_extra_ms;  // virtual ms burned by I_Sleep, see DG_SleepMs
static uint32_t s_buttons;   // button mask currently held down
static int32_t s_booted;

// Bit N of the button mask is held down iff s_keymap[N] is held down. Keeping
// this a flat table means the host protobuf only ever carries a uint32.
static const unsigned char s_keymap[32] = {
    [0]  = KEY_UPARROW,
    [1]  = KEY_DOWNARROW,
    [2]  = KEY_LEFTARROW,
    [3]  = KEY_RIGHTARROW,
    [4]  = KEY_STRAFE_L,
    [5]  = KEY_STRAFE_R,
    [6]  = KEY_FIRE,
    [7]  = KEY_USE,
    [8]  = KEY_RSHIFT,
    [9]  = KEY_RALT,
    [10] = KEY_ESCAPE,
    [11] = KEY_ENTER,
    [12] = KEY_TAB,
    [13] = 'y',
    [14] = 'n',
    [15] = '1',
    [16] = '2',
    [17] = '3',
    [18] = '4',
    [19] = '5',
    [20] = '6',
    [21] = '7',
};

static void queue_key(int pressed, unsigned char key)
{
    s_keyqueue[s_keyqueue_wr] = (unsigned short)((pressed << 8) | key);
    s_keyqueue_wr = (s_keyqueue_wr + 1) % KEYQUEUE_SIZE;
}

// Turn an edge-triggered mask diff into the key up/down events DOOM expects.
static void apply_buttons(uint32_t buttons)
{
    uint32_t changed = buttons ^ s_buttons;
    int i;

    for (i = 0; i < 32; i++)
    {
        if (!(changed & (1u << i)) || s_keymap[i] == 0)
            continue;

        queue_key((buttons >> i) & 1u, s_keymap[i]);
    }

    s_buttons = buttons;
}

//
// DG_* platform hooks
//

void DG_Init(void)
{
}

void DG_DrawFrame(void)
{
    // I_FinishUpdate already blitted into DG_ScreenBuffer; the host reads it
    // straight out of linear memory.
}

// DOOM sleeps while it waits for the clock to catch up, most notably inside the
// screen wipe. Advancing virtual time here is what makes those loops terminate.
void DG_SleepMs(uint32_t ms)
{
    s_extra_ms += ms ? ms : 1;
}

uint32_t DG_GetTicksMs(void)
{
    return (uint32_t)(TIC_MS(s_tic) + s_extra_ms);
}

int DG_GetKey(int *pressed, unsigned char *doomKey)
{
    unsigned short keydata;

    if (s_keyqueue_rd == s_keyqueue_wr)
        return 0;

    keydata = s_keyqueue[s_keyqueue_rd];
    s_keyqueue_rd = (s_keyqueue_rd + 1) % KEYQUEUE_SIZE;

    *pressed = keydata >> 8;
    *doomKey = keydata & 0xff;

    return 1;
}

void DG_SetWindowTitle(const char *title)
{
    (void)title;
}

//
// host ABI
//

EXPORT("chain_init")
int32_t chain_init(void)
{
    static char *argv[] = {
        "doom",
        "-iwad", "/doom.wad",
        "-nosound",
        "-nomusic",
    };

    if (s_booted)
        return 1;

    // Run exactly one tic per TryRunTics() instead of chasing a wall clock.
    singletics = true;
    usemouse = 0;

    doomgeneric_Create(sizeof(argv) / sizeof(argv[0]), argv);

    s_booted = 1;
    return 0;
}

EXPORT("chain_tic")
void chain_tic(uint32_t buttons)
{
    s_tic++;
    apply_buttons(buttons);
    doomgeneric_Tick();
}

EXPORT("chain_framebuffer")
uint32_t chain_framebuffer(void)
{
    return (uint32_t)(uintptr_t)DG_ScreenBuffer;
}

EXPORT("chain_palette")
uint32_t chain_palette(void)
{
    return (uint32_t)(uintptr_t)colors;
}

EXPORT("chain_tic_count")
uint32_t chain_tic_count(void)
{
    return s_tic;
}
