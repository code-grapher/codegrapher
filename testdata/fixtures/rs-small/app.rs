// app uses the Circle shape: constructs one and calls a trait method.
use crate::shapes::Circle;
use crate::shapes::Shape;

pub mod app {
    use crate::shapes::Circle;

    /// run builds a Circle and reports its area via the Shape trait.
    pub fn run() -> f64 {
        let c = Circle::new(2.0);
        let a = c.area();
        let lit = Circle { radius: 1.0 };
        let _ = lit.label();
        a
    }
}
