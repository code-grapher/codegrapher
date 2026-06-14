<?php
// Domain models for the php-small fixture.

namespace App\Animals;

interface Speaker {
    public function speak(): string;
}

trait Walkable {
    public function walk(): string {
        return "walking";
    }
}

enum Size: string {
    case Small = "small";
    case Large = "large";
}

abstract class Animal implements Speaker {
    protected string $name = "";

    public function __construct(string $name) {
        $this->name = $name;
    }

    public function speak(): string {
        return "...";
    }
}

class Dog extends Animal {
    use Walkable;

    public string $breed = "";

    public function __construct(string $name, string $breed) {
        $this->name = $name;
        $this->breed = $breed;
    }

    public function speak(): string {
        return "woof";
    }
}
