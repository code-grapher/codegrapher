import 'shapes.dart';

String describe(Shape s) => s.label();

class App {
  double run() {
    var c = Circle('unit', 1.0);
    var u = Circle.unit();
    var a = c.area();
    describe(c);
    return a;
  }
}
