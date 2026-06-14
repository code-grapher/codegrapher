<?php
// Service layer exercising use-imports + new + inferred instance-method calls.

namespace App\Service;

use App\Animals\Dog;

const MAX_DOGS = 10;

function make_dog(string $name): Dog {
    return new Dog($name, "lab");
}

function describe(string $name): string {
    $d = new Dog($name, "lab");
    $sound = $d->speak();
    $d->walk();
    return $sound;
}
