#ifndef NIUMA_COMPUTER_PANEL_DARWIN_H
#define NIUMA_COMPUTER_PANEL_DARWIN_H

#include <stdbool.h>

#ifdef __OBJC__
#import <AppKit/AppKit.h>
#import <Foundation/Foundation.h>
#else
typedef void NSPanel;
#endif

#ifdef __cplusplus
extern "C" {
#endif

typedef struct goe2e_computer_panel goe2e_computer_panel;

goe2e_computer_panel *goe2e_computer_panel_create(void);
void goe2e_computer_panel_update(goe2e_computer_panel *panel, const char *json_utf8);
char *goe2e_computer_panel_poll(goe2e_computer_panel *panel);
void goe2e_computer_panel_free_string(char *value);
void goe2e_computer_panel_close(goe2e_computer_panel *panel);

#ifdef __cplusplus
}
#endif

#endif
