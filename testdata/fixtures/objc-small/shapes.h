// shapes.h declares a Drawable protocol, an abstract Shape base, and a Circle
// subclass that adopts the protocol.
#import <Foundation/Foundation.h>

// Drawable is adopted by shapes that can render a label.
@protocol Drawable <NSObject>
- (NSString *)label;
@end

// Shape is the base of all shapes.
@interface Shape : NSObject {
    NSString *_name;
}
@property (nonatomic, copy) NSString *name;
- (double)area;
@end

// Circle is a concrete Shape that is Drawable.
@interface Circle : Shape <Drawable>
+ (instancetype)circleWithRadius:(double)radius;
- (double)area;
- (NSString *)label;
@end
