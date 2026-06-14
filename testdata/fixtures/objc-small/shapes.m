// shapes.m implements Shape and Circle and a driver that constructs a Circle
// and exercises a message-send.
#import "shapes.h"

@implementation Shape
- (double)area {
    return 0.0;
}
@end

@implementation Circle
+ (instancetype)circleWithRadius:(double)radius {
    return [[Circle alloc] init];
}
- (double)area {
    return 3.14;
}
- (NSString *)label {
    return [self name];
}
@end

// run constructs a Circle and exercises the virtual call.
double run(void) {
    Circle *c = [[Circle alloc] init];
    double a = [c area];
    return a;
}
