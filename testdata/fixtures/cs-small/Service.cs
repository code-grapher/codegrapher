using System;
using CsSmall.Models;

namespace CsSmall.App
{
    public class Service
    {
        public Dog MakeDog()
        {
            return new Dog();
        }

        public string Describe()
        {
            var d = new Dog();
            return d.Speak();
        }
    }
}
