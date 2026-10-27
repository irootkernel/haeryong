#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include <stdlib.h>

// Local fixture shim. The pinned dependency and its original module are intact.
@interface HYGuard : NSObject <WKScriptMessageHandler,WKNavigationDelegate,WKUIDelegate>
@property(nonatomic,strong) id original;
@property(nonatomic,strong) NSURL *trusted;
@property(nonatomic,strong) NSMutableArray *popups;
@property(nonatomic,strong) NSMutableArray *popupURLs;
@property(nonatomic,strong) NSMutableArray *senders;
@property(nonatomic) NSUInteger totalPopups;
@property(nonatomic) NSUInteger accepted;
@property(nonatomic) NSUInteger denied;
@property(nonatomic) NSUInteger navigationsDenied;
@property(nonatomic) BOOL tabFocusesLinks;
@end
static HYGuard *guard;
static WKWebView *findWebView(NSView *view){if([view isKindOfClass:[WKWebView class]])return (WKWebView*)view;for(NSView *child in view.subviews){WKWebView *found=findWebView(child);if(found)return found;}return nil;}
static BOOL sameOrigin(NSURL *a,NSURL *b){return [a.scheme isEqualToString:b.scheme]&&[a.host isEqualToString:b.host]&&((a.port==nil&&b.port==nil)||[a.port isEqual:b.port]);}
@implementation HYGuard
- (BOOL)respondsToSelector:(SEL)s{return [super respondsToSelector:s]||[self.original respondsToSelector:s];}
- (id)forwardingTargetForSelector:(SEL)s{return [self.original respondsToSelector:s]?self.original:[super forwardingTargetForSelector:s];}
- (void)userContentController:(WKUserContentController*)controller didReceiveScriptMessage:(WKScriptMessage*)message {
 WKSecurityOrigin *sender=message.frameInfo.securityOrigin;
 BOOL origin=[sender.protocol isEqualToString:self.trusted.scheme]&&[sender.host isEqualToString:self.trusted.host]&&sender.port==self.trusted.port.integerValue;
 BOOL allowed=message.frameInfo.isMainFrame&&origin;
 if(self.senders.count<16)[self.senders addObject:@{@"main_frame":@(message.frameInfo.isMainFrame),@"scheme":sender.protocol?:@"",@"host":sender.host?:@"",@"port":@(sender.port),@"allowed":@(allowed)}];
 if(!allowed){self.denied++;return;}self.accepted++;
 [self.original userContentController:controller didReceiveScriptMessage:message];
}
- (void)webView:(WKWebView*)view decidePolicyForNavigationAction:(WKNavigationAction*)action decisionHandler:(void (^)(WKNavigationActionPolicy))decision {
 if(action.targetFrame.isMainFrame&&!sameOrigin(action.request.URL,self.trusted)){self.navigationsDenied++;decision(WKNavigationActionPolicyCancel);return;}
 decision(WKNavigationActionPolicyAllow);
}
- (void)webViewDidClose:(WKWebView*)view {for(NSWindow *window in [self.popups copy]){if(window.contentView==view){[window close];[self.popups removeObject:window];break;}}}
- (WKWebView*)webView:(WKWebView*)view createWebViewWithConfiguration:(WKWebViewConfiguration*)plain forNavigationAction:(WKNavigationAction*)action windowFeatures:(WKWindowFeatures*)features {
 // WKUIDelegate requires the supplied configuration copy. Replace its shared
 // privileged controller before construction. Copied native scheme requests
 // remain disabled by the app middleware.
 plain.userContentController=[WKUserContentController new];
 // Record the exact popup target URL for harness verification; content is
 // never inspected, only the requested navigation target.
 if(self.popupURLs.count<8)[self.popupURLs addObject:action.request.URL.absoluteString?:@""];
 // This WebKit rejects replacing an already registered scheme handler, even
 // with nil. Every copied wails:// request is denied by the app middleware.
 WKWebView *popup=[[WKWebView alloc]initWithFrame:NSMakeRect(0,0,720,520) configuration:plain];
 popup.UIDelegate=self;
 NSWindow *window=[[NSWindow alloc]initWithContentRect:NSMakeRect(80,80,720,520) styleMask:NSWindowStyleMaskTitled|NSWindowStyleMaskClosable|NSWindowStyleMaskResizable backing:NSBackingStoreBuffered defer:NO];
 window.title=@"Unprivileged artifact popup";window.contentView=popup;window.releasedWhenClosed=NO;[self.popups addObject:window];self.totalPopups++;[window makeKeyAndOrderFront:nil];return popup;
}
@end
int hy_install(void *pointer,const char *expected){
 NSWindow *window=(__bridge NSWindow*)pointer;WKWebView *view=findWebView(window.contentView);if(!view)return 0;
 if(guard)return sameOrigin(guard.trusted,[NSURL URLWithString:[NSString stringWithUTF8String:expected]])?1:0;
 HYGuard *next=[HYGuard new];next.original=view.navigationDelegate;next.tabFocusesLinks=view.configuration.preferences.tabFocusesLinks;next.trusted=[NSURL URLWithString:[NSString stringWithUTF8String:expected]];next.popups=[NSMutableArray new];next.popupURLs=[NSMutableArray new];next.senders=[NSMutableArray new];
 if(![next.original respondsToSelector:@selector(userContentController:didReceiveScriptMessage:)])return 0;
 [view.configuration.userContentController removeScriptMessageHandlerForName:@"external"];
 [view.configuration.userContentController addScriptMessageHandler:next name:@"external"];
 view.navigationDelegate=next;view.UIDelegate=next;guard=next;return 1;
}
void hy_eval(void *pointer,const char *script){WKWebView *view=findWebView(((__bridge NSWindow*)pointer).contentView);NSString *code=[NSString stringWithUTF8String:script];[view evaluateJavaScript:code completionHandler:^(id result,NSError *error){if(error)NSLog(@"Haeryong fixture script evaluation failed: %@ (script length %lu)",error.localizedDescription,(unsigned long)[code length]);}];}
char *hy_stats(void){NSDictionary *value=@{@"installed":@(guard!=nil),@"effective_tab_focuses_links":@(guard.tabFocusesLinks),@"accepted_main_messages":@(guard.accepted),@"denied_messages":@(guard.denied),@"denied_top_navigations":@(guard.navigationsDenied),@"plain_popups":@(guard.totalPopups),@"active_popups":@(guard.popups.count),@"popup_urls":guard.popupURLs?:@[],@"senders":guard.senders?:@[]};NSData *data=[NSJSONSerialization dataWithJSONObject:value options:0 error:nil];return strdup([[NSString alloc]initWithData:data encoding:NSUTF8StringEncoding].UTF8String);}
